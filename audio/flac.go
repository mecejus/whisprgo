package audio

import (
	"bytes"
	"fmt"
	"io"
)

// This file is a small, pure-Go FLAC encoder for 16-bit mono PCM. It exists
// because the upload to the transcription API is streamed while the user is
// still speaking, and that needs an encoder that can emit the stream frame by
// frame as samples arrive. CoreAudio's ExtAudioFile encoder only writes whole
// files to disk, so it could not do that.
//
// The encoder uses FLAC's fixed polynomial predictors (orders 0-4) with
// Rice-coded residuals, which is what `flac -0` does. Lossless is the point:
// transcription accuracy is not worth trading for a smaller upload, and the
// upload happens during the recording anyway, so its size barely matters.

const (
	// flacBlockSize is the number of samples per frame: 256 ms at 16 kHz.
	// Each frame is written to the network as soon as it is complete, so this
	// is also the granularity at which audio leaves the machine.
	flacBlockSize = 4096

	maxRiceParam      = 14 // 4-bit Rice parameters; 15 is the escape code
	maxPartitionOrder = 6
)

var (
	crc8Table  [256]uint8
	crc16Table [256]uint16
)

func init() {
	for i := range crc8Table {
		c := uint8(i)
		for range 8 {
			if c&0x80 != 0 {
				c = c<<1 ^ 0x07
			} else {
				c <<= 1
			}
		}
		crc8Table[i] = c
	}
	for i := range crc16Table {
		c := uint16(i) << 8
		for range 8 {
			if c&0x8000 != 0 {
				c = c<<1 ^ 0x8005
			} else {
				c <<= 1
			}
		}
		crc16Table[i] = c
	}
}

func crc8(b []byte) uint8 {
	var c uint8
	for _, x := range b {
		c = crc8Table[c^x]
	}
	return c
}

func crc16(b []byte) uint16 {
	var c uint16
	for _, x := range b {
		c = c<<8 ^ crc16Table[uint8(c>>8)^x]
	}
	return c
}

// bitWriter accumulates bits MSB-first into a byte slice.
type bitWriter struct {
	buf  []byte
	acc  uint64
	nacc uint
}

func (b *bitWriter) reset() {
	b.buf = b.buf[:0]
	b.acc = 0
	b.nacc = 0
}

// writeBits appends the low n bits of v, most significant first.
func (b *bitWriter) writeBits(v uint64, n uint) {
	for n > 0 {
		take := min(n, 56)
		chunk := (v >> (n - take)) & (1<<take - 1)
		b.acc = b.acc<<take | chunk
		b.nacc += take
		n -= take
		for b.nacc >= 8 {
			b.nacc -= 8
			b.buf = append(b.buf, byte(b.acc>>b.nacc))
		}
	}
}

// writeUnary writes q zero bits followed by a one.
func (b *bitWriter) writeUnary(q uint32) {
	for q >= 32 {
		b.writeBits(0, 32)
		q -= 32
	}
	b.writeBits(1, uint(q)+1)
}

// writeUTF8 writes v in FLAC's UTF-8-style variable-length integer coding.
func (b *bitWriter) writeUTF8(v uint32) {
	switch {
	case v < 0x80:
		b.writeBits(uint64(v), 8)
	case v < 0x800:
		b.writeBits(uint64(0xC0|v>>6), 8)
		b.writeBits(uint64(0x80|v&0x3F), 8)
	case v < 0x10000:
		b.writeBits(uint64(0xE0|v>>12), 8)
		b.writeBits(uint64(0x80|v>>6&0x3F), 8)
		b.writeBits(uint64(0x80|v&0x3F), 8)
	case v < 0x200000:
		b.writeBits(uint64(0xF0|v>>18), 8)
		b.writeBits(uint64(0x80|v>>12&0x3F), 8)
		b.writeBits(uint64(0x80|v>>6&0x3F), 8)
		b.writeBits(uint64(0x80|v&0x3F), 8)
	case v < 0x4000000:
		b.writeBits(uint64(0xF8|v>>24), 8)
		b.writeBits(uint64(0x80|v>>18&0x3F), 8)
		b.writeBits(uint64(0x80|v>>12&0x3F), 8)
		b.writeBits(uint64(0x80|v>>6&0x3F), 8)
		b.writeBits(uint64(0x80|v&0x3F), 8)
	default:
		b.writeBits(uint64(0xFC|v>>30), 8)
		b.writeBits(uint64(0x80|v>>24&0x3F), 8)
		b.writeBits(uint64(0x80|v>>18&0x3F), 8)
		b.writeBits(uint64(0x80|v>>12&0x3F), 8)
		b.writeBits(uint64(0x80|v>>6&0x3F), 8)
		b.writeBits(uint64(0x80|v&0x3F), 8)
	}
}

func (b *bitWriter) align() {
	if b.nacc > 0 {
		b.writeBits(0, 8-b.nacc)
	}
}

// FLACWriter encodes 16-bit mono PCM at SampleRate into a FLAC stream one
// frame at a time. Samples may be supplied in chunks of any size; complete
// frames are written to the underlying writer as soon as they can be, and
// Close flushes whatever is left as a final, shorter frame.
type FLACWriter struct {
	w       io.Writer
	total   int64 // for STREAMINFO; 0 when unknown (streaming)
	started bool
	closed  bool
	frames  uint32
	pending []int16

	bw        bitWriter
	residual  [5][]int32 // per fixed predictor order
	zigzag    []uint32
	riceParam []uint
}

// NewFLACWriter returns a writer that streams FLAC to w. The stream header
// declares the total length as unknown, which every decoder handles; use
// EncodeFLAC when the whole recording is already in hand.
func NewFLACWriter(w io.Writer) *FLACWriter {
	return newFLACWriter(w, 0)
}

func newFLACWriter(w io.Writer, total int64) *FLACWriter {
	f := &FLACWriter{
		w:         w,
		total:     total,
		pending:   make([]int16, 0, 2*flacBlockSize),
		zigzag:    make([]uint32, flacBlockSize),
		riceParam: make([]uint, 1<<maxPartitionOrder),
	}
	for i := range f.residual {
		f.residual[i] = make([]int32, flacBlockSize)
	}
	f.bw.buf = make([]byte, 0, 2*flacBlockSize+64)
	return f
}

func (f *FLACWriter) writeHeader() error {
	f.started = true
	b := &f.bw
	b.reset()
	b.buf = append(b.buf, 'f', 'L', 'a', 'C')
	b.writeBits(1, 1)   // last metadata block
	b.writeBits(0, 7)   // STREAMINFO
	b.writeBits(34, 24) // length
	b.writeBits(flacBlockSize, 16)
	b.writeBits(flacBlockSize, 16)
	b.writeBits(0, 24) // min frame size: unknown
	b.writeBits(0, 24) // max frame size: unknown
	b.writeBits(SampleRate, 20)
	b.writeBits(channels-1, 3)
	b.writeBits(16-1, 5)
	b.writeBits(uint64(f.total), 36)
	b.writeBits(0, 64) // MD5 of the PCM: unset
	b.writeBits(0, 64)
	_, err := f.w.Write(b.buf)
	return err
}

// Write queues samples and writes every complete frame they finish.
func (f *FLACWriter) Write(samples []int16) error {
	if f.closed {
		return fmt.Errorf("flac: write after close")
	}
	if !f.started {
		if err := f.writeHeader(); err != nil {
			return err
		}
	}
	f.pending = append(f.pending, samples...)
	for len(f.pending) >= flacBlockSize {
		if err := f.writeFrame(f.pending[:flacBlockSize]); err != nil {
			return err
		}
		n := copy(f.pending, f.pending[flacBlockSize:])
		f.pending = f.pending[:n]
	}
	return nil
}

// Close writes the final partial frame, if any. It does not close the
// underlying writer. A stream with no samples at all is an error.
func (f *FLACWriter) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	if !f.started {
		return fmt.Errorf("flac: no samples")
	}
	if len(f.pending) == 0 {
		return nil
	}
	err := f.writeFrame(f.pending)
	f.pending = f.pending[:0]
	return err
}

func (f *FLACWriter) writeFrame(samples []int16) error {
	f.encodeFrame(samples)
	f.frames++
	_, err := f.w.Write(f.bw.buf)
	return err
}

// encodeFrame builds one complete frame in f.bw.
func (f *FLACWriter) encodeFrame(samples []int16) {
	n := len(samples)
	b := &f.bw
	b.reset()

	// Frame header.
	b.writeBits(0x3FFE, 14) // sync code
	b.writeBits(0, 1)       // reserved
	b.writeBits(0, 1)       // fixed block size; header carries a frame number
	bsCode, bsExtra, bsExtraBits := blockSizeCode(n)
	b.writeBits(bsCode, 4)
	srCode, srExtra, srExtraBits := sampleRateCode(SampleRate)
	b.writeBits(srCode, 4)
	b.writeBits(0, 4)     // one channel
	b.writeBits(0b100, 3) // 16 bits per sample
	b.writeBits(0, 1)     // reserved
	b.writeUTF8(f.frames)
	if bsExtraBits > 0 {
		b.writeBits(bsExtra, bsExtraBits)
	}
	if srExtraBits > 0 {
		b.writeBits(srExtra, srExtraBits)
	}
	b.writeBits(uint64(crc8(b.buf)), 8)

	f.encodeSubframe(samples)

	b.align()
	b.writeBits(uint64(crc16(b.buf)), 16)
}

func blockSizeCode(n int) (code, extra uint64, extraBits uint) {
	switch n {
	case 192:
		return 1, 0, 0
	case 576:
		return 2, 0, 0
	case 1152:
		return 3, 0, 0
	case 2304:
		return 4, 0, 0
	case 4608:
		return 5, 0, 0
	case 256:
		return 8, 0, 0
	case 512:
		return 9, 0, 0
	case 1024:
		return 10, 0, 0
	case 2048:
		return 11, 0, 0
	case 4096:
		return 12, 0, 0
	case 8192:
		return 13, 0, 0
	case 16384:
		return 14, 0, 0
	case 32768:
		return 15, 0, 0
	}
	if n <= 256 {
		return 6, uint64(n - 1), 8
	}
	return 7, uint64(n - 1), 16
}

func sampleRateCode(rate int) (code, extra uint64, extraBits uint) {
	switch rate {
	case 88200:
		return 1, 0, 0
	case 176400:
		return 2, 0, 0
	case 192000:
		return 3, 0, 0
	case 8000:
		return 4, 0, 0
	case 16000:
		return 5, 0, 0
	case 22050:
		return 6, 0, 0
	case 24000:
		return 7, 0, 0
	case 32000:
		return 8, 0, 0
	case 44100:
		return 9, 0, 0
	case 48000:
		return 10, 0, 0
	case 96000:
		return 11, 0, 0
	}
	if rate < 1<<16 {
		return 13, uint64(rate), 16
	}
	return 0, 0, 0 // decoder takes it from STREAMINFO
}

// encodeSubframe picks the cheapest representation of the block and writes
// it: a constant, verbatim samples, or a fixed predictor with Rice-coded
// residuals.
func (f *FLACWriter) encodeSubframe(samples []int16) {
	n := len(samples)
	b := &f.bw

	constant := true
	for _, s := range samples[1:] {
		if s != samples[0] {
			constant = false
			break
		}
	}
	if constant {
		b.writeBits(0, 1)
		b.writeBits(0, 6) // CONSTANT
		b.writeBits(0, 1) // no wasted bits
		b.writeBits(uint64(uint16(samples[0])), 16)
		return
	}

	// Fixed predictors: choose the order with the smallest residual energy
	// (sum of magnitudes is the usual, cheap proxy), then find the best Rice
	// partitioning for it exactly.
	bestOrder, bestSum := -1, uint64(0)
	for order := 0; order <= 4 && order < n; order++ {
		res := f.residual[order][:n-order]
		fixedResidual(order, samples, res)
		var sum uint64
		for _, r := range res {
			if r < 0 {
				sum += uint64(-r)
			} else {
				sum += uint64(r)
			}
		}
		if bestOrder < 0 || sum < bestSum {
			bestOrder, bestSum = order, sum
		}
	}
	order := bestOrder
	res := f.residual[order][:n-order]
	u := f.zigzag[:n-order]
	for i, r := range res {
		u[i] = uint32(r<<1 ^ r>>31)
	}

	partOrder, bits := f.bestPartitioning(n, order, u)
	bits += 16*order + 2 + 4

	if bits >= 16*n {
		// Incompressible (noise, or a tiny block). Store the samples as is.
		b.writeBits(0, 1)
		b.writeBits(1, 6) // VERBATIM
		b.writeBits(0, 1)
		for _, s := range samples {
			b.writeBits(uint64(uint16(s)), 16)
		}
		return
	}

	b.writeBits(0, 1)
	b.writeBits(uint64(0b001000|order), 6) // FIXED, order
	b.writeBits(0, 1)
	for _, s := range samples[:order] {
		b.writeBits(uint64(uint16(s)), 16)
	}
	b.writeBits(0, 2) // residual coding: 4-bit Rice parameters
	b.writeBits(uint64(partOrder), 4)

	partitions := 1 << partOrder
	partSize := n >> partOrder
	lo := 0
	for p := range partitions {
		hi := (p+1)*partSize - order
		k := f.riceParam[p]
		b.writeBits(uint64(k), 4)
		mask := uint64(1)<<k - 1
		for _, v := range u[lo:hi] {
			b.writeUnary(v >> k)
			b.writeBits(uint64(v)&mask, k)
		}
		lo = hi
	}
}

// fixedResidual fills res with the order-n fixed predictor residuals for
// samples[order:].
func fixedResidual(order int, x []int16, res []int32) {
	switch order {
	case 0:
		for i := range res {
			res[i] = int32(x[i])
		}
	case 1:
		for i := range res {
			res[i] = int32(x[i+1]) - int32(x[i])
		}
	case 2:
		for i := range res {
			res[i] = int32(x[i+2]) - 2*int32(x[i+1]) + int32(x[i])
		}
	case 3:
		for i := range res {
			res[i] = int32(x[i+3]) - 3*int32(x[i+2]) + 3*int32(x[i+1]) - int32(x[i])
		}
	case 4:
		for i := range res {
			res[i] = int32(x[i+4]) - 4*int32(x[i+3]) + 6*int32(x[i+2]) - 4*int32(x[i+1]) + int32(x[i])
		}
	}
}

// bestPartitioning finds the Rice partition order that codes u in the
// fewest bits, leaving the per-partition parameters in f.riceParam. Returns
// the order and the bit count of the residual section (excluding its 6-bit
// header).
func (f *FLACWriter) bestPartitioning(n, order int, u []uint32) (partOrder int, bits int) {
	var scratch [1 << maxPartitionOrder]uint
	bestBits := -1
	for po := 0; po <= maxPartitionOrder; po++ {
		partitions := 1 << po
		partSize := n >> po
		if partSize<<po != n || partSize <= order {
			break
		}
		total := 0
		lo := 0
		for p := range partitions {
			hi := (p+1)*partSize - order
			k, cost := bestRice(u[lo:hi], bestBits-total)
			scratch[p] = k
			total += 4 + cost
			lo = hi
			if bestBits >= 0 && total >= bestBits {
				break
			}
		}
		if bestBits < 0 || total < bestBits {
			bestBits = total
			partOrder = po
			copy(f.riceParam, scratch[:partitions])
		}
	}
	return partOrder, bestBits
}

// bestRice returns the Rice parameter that codes u in the fewest bits, and
// that bit count. limit, if non-negative, lets the search stop as soon as no
// parameter can beat it.
func bestRice(u []uint32, limit int) (uint, int) {
	bestK, bestBits := uint(0), -1
	for k := uint(0); k <= maxRiceParam; k++ {
		bits := int(k+1) * len(u)
		if bestBits >= 0 && bits >= bestBits {
			break // the fixed part alone already costs more
		}
		if limit >= 0 && bits >= limit {
			if bestBits < 0 {
				// Nothing evaluated yet; report a lower bound that the
				// caller will reject rather than a bogus zero.
				bestBits = bits
			}
			break
		}
		for _, v := range u {
			bits += int(v >> k)
		}
		if bestBits < 0 || bits < bestBits {
			bestK, bestBits = k, bits
		}
	}
	return bestK, bestBits
}

// EncodeFLAC compresses a complete recording losslessly. Encoding a minute of
// 16 kHz audio takes a few milliseconds.
func EncodeFLAC(samples []int16) ([]byte, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("no samples")
	}
	var buf bytes.Buffer
	buf.Grow(len(samples) + 64)
	w := newFLACWriter(&buf, int64(len(samples)))
	if err := w.Write(samples); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
