package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tone builds a two-harmonic sine wave, compressible enough to be a fair
// stand-in for speech without being so trivial that a broken encoder would
// still look like it shrank the data.
func tone(seconds int) []int16 {
	return toneN(SampleRate * seconds)
}

func toneN(n int) []int16 {
	s := make([]int16, n)
	for i := range s {
		t := float64(i) / float64(SampleRate)
		v := math.Sin(2*math.Pi*220*t)*8000 + math.Sin(2*math.Pi*440*t)*4000
		s[i] = int16(v)
	}
	return s
}

func noise(n int, seed int64) []int16 {
	r := rand.New(rand.NewSource(seed))
	s := make([]int16, n)
	for i := range s {
		s[i] = int16(r.Intn(65536) - 32768)
	}
	return s
}

// speechLike mixes silence, tone and bursts of noise with hard edges, which
// pushes the encoder through every subframe type and predictor order.
func speechLike(n int) []int16 {
	s := make([]int16, n)
	r := rand.New(rand.NewSource(7))
	t := toneN(n)
	for i := range s {
		switch (i / 3000) % 4 {
		case 0:
			s[i] = 0
		case 1:
			s[i] = t[i]
		case 2:
			s[i] = int16(r.Intn(2000) - 1000)
		case 3:
			if r.Intn(50) == 0 {
				s[i] = int16(r.Intn(65536) - 32768) // clicks: worst case for prediction
			} else {
				s[i] = t[i] / 4
			}
		}
	}
	s[0], s[1] = math.MinInt16, math.MaxInt16
	return s
}

// decodeExternally runs the FLAC bytes through a decoder we did not write:
// afconvert on macOS (including CI), the reference `flac` tool elsewhere.
func decodeExternally(t *testing.T, flac []byte) []int16 {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.flac")
	dst := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(src, flac, 0o600); err != nil {
		t.Fatal(err)
	}

	var cmd *exec.Cmd
	if _, err := exec.LookPath("afconvert"); err == nil {
		cmd = exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", src, dst)
	} else if _, err := exec.LookPath("flac"); err == nil {
		cmd = exec.Command("flac", "-d", "-s", "-f", "-o", dst, src)
	} else {
		t.Skip("no external FLAC decoder (afconvert or flac) available")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", cmd.Args[0], err, out)
	}

	decoded, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := pcmFromWAV(decoded)
	if err != nil {
		t.Fatalf("decoded WAV: %v", err)
	}
	return pcm
}

func assertSameSamples(t *testing.T, got, want []int16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("decoded %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d changed: got %d, want %d", i, got[i], want[i])
		}
	}
}

// The whole reason for choosing FLAC over a lossy codec is that transcription
// accuracy is untouched. Decode what we produced with an independent decoder
// and prove the samples survive the round trip exactly, across every kind of
// block the encoder can emit.
func TestEncodeFLACIsLossless(t *testing.T) {
	cases := map[string][]int16{
		"tone":             tone(3),
		"silence":          make([]int16, SampleRate*2),
		"noise":            noise(SampleRate, 1),
		"speech-like":      speechLike(SampleRate * 4),
		"tiny":             toneN(100),
		"one-block":        toneN(flacBlockSize),
		"one-block-plus-1": toneN(flacBlockSize + 1),
		"short-last-block": toneN(flacBlockSize*3 + 1234),
	}
	cases["constant-nonzero"] = make([]int16, 5000)
	for i := range cases["constant-nonzero"] {
		cases["constant-nonzero"][i] = -1234
	}
	sq := make([]int16, 9000)
	for i := range sq {
		if (i/37)%2 == 0 {
			sq[i] = math.MaxInt16
		} else {
			sq[i] = math.MinInt16
		}
	}
	cases["full-scale-square"] = sq

	for name, samples := range cases {
		t.Run(name, func(t *testing.T) {
			flac, err := EncodeFLAC(samples)
			if err != nil {
				t.Fatalf("EncodeFLAC: %v", err)
			}
			if !bytes.HasPrefix(flac, []byte("fLaC")) {
				t.Fatalf("output is not FLAC, first bytes: %x", flac[:min(8, len(flac))])
			}
			assertSameSamples(t, decodeExternally(t, flac), samples)
			t.Logf("%d samples: PCM %d bytes -> FLAC %d bytes (%.0f%%)",
				len(samples), 2*len(samples), len(flac), 100*float64(len(flac))/float64(2*len(samples)))
		})
	}
}

func TestEncodeFLACIsSmaller(t *testing.T) {
	samples := tone(3)
	flac, err := EncodeFLAC(samples)
	if err != nil {
		t.Fatal(err)
	}
	if ratio := float64(len(flac)) / float64(2*len(samples)); ratio > 0.7 {
		t.Fatalf("FLAC is %.0f%% of PCM; expected real compression", 100*ratio)
	}
}

func TestEncodeFLACRejectsEmpty(t *testing.T) {
	if _, err := EncodeFLAC(nil); err == nil {
		t.Error("expected an error for empty input")
	}
	if err := NewFLACWriter(&bytes.Buffer{}).Close(); err == nil {
		t.Error("expected an error closing a writer that saw no samples")
	}
}

// chunkWriter records every Write so a test can see when frames leave.
type chunkWriter struct {
	bytes.Buffer
	writes []int
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	c.writes = append(c.writes, len(p))
	return c.Buffer.Write(p)
}

// The stream must leave as it is encoded: a frame's bytes are handed to the
// writer the moment the frame is complete, not when the stream closes.
func TestFLACWriterEmitsFramesIncrementally(t *testing.T) {
	samples := toneN(flacBlockSize*2 + 500)
	var cw chunkWriter
	w := NewFLACWriter(&cw)

	if err := w.Write(samples[:1000]); err != nil {
		t.Fatal(err)
	}
	if len(cw.writes) != 1 { // header only
		t.Fatalf("after 1000 samples: %d writes, want 1 (header)", len(cw.writes))
	}
	if err := w.Write(samples[1000:flacBlockSize]); err != nil {
		t.Fatal(err)
	}
	if len(cw.writes) != 2 {
		t.Fatalf("after one block: %d writes, want 2", len(cw.writes))
	}
	if err := w.Write(samples[flacBlockSize:]); err != nil {
		t.Fatal(err)
	}
	if len(cw.writes) != 3 {
		t.Fatalf("after two blocks and a remainder: %d writes, want 3", len(cw.writes))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if len(cw.writes) != 4 {
		t.Fatalf("after close: %d writes, want 4", len(cw.writes))
	}
	assertSameSamples(t, decodeExternally(t, cw.Bytes()), samples)
}

// Chunk boundaries must not affect the output: streamed audio arrives in
// whatever sizes the audio queue hands over.
func TestFLACWriterChunkingIsTransparent(t *testing.T) {
	samples := speechLike(SampleRate * 3)

	var whole bytes.Buffer
	w := NewFLACWriter(&whole)
	if err := w.Write(samples); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	var pieces bytes.Buffer
	w = NewFLACWriter(&pieces)
	r := rand.New(rand.NewSource(3))
	for off := 0; off < len(samples); {
		n := min(1+r.Intn(3000), len(samples)-off)
		if err := w.Write(samples[off : off+n]); err != nil {
			t.Fatal(err)
		}
		off += n
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(whole.Bytes(), pieces.Bytes()) {
		t.Fatal("streamed output differs from one-shot output")
	}
}

// pcmFromWAV walks the RIFF chunks to find "data" rather than assuming a
// 44-byte header; afconvert emits extra chunks.
func pcmFromWAV(b []byte) ([]int16, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, os.ErrInvalid
	}
	for off := 12; off+8 <= len(b); {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		body := off + 8
		if body+size > len(b) {
			size = len(b) - body
		}
		if id == "data" {
			out := make([]int16, size/2)
			if err := binary.Read(bytes.NewReader(b[body:body+size]), binary.LittleEndian, &out); err != nil {
				return nil, err
			}
			return out, nil
		}
		off = body + size + size%2
	}
	return nil, os.ErrInvalid
}

func BenchmarkEncodeFLACMinute(b *testing.B) {
	samples := speechLike(SampleRate * 60)
	b.SetBytes(int64(2 * len(samples)))
	b.ResetTimer()
	for range b.N {
		if _, err := EncodeFLAC(samples); err != nil {
			b.Fatal(err)
		}
	}
}
