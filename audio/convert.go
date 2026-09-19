package audio

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Shared-mode WASAPI hands over whatever format the audio engine is already
// mixing in — commonly 48 kHz stereo float32 — and does not convert for you
// unless the stream was opened with AUTOCONVERT. macOS's AudioQueue does that
// conversion itself, which is why nothing like this file exists for it.
//
// This is where the Windows recorder lands a capture buffer in whatever the
// device gave us and gets back the 16 kHz mono int16 the FLAC encoder wants.
// It is plain Go with no syscalls, so it is built and tested on every
// platform rather than only where it runs; the linker drops it from the macOS
// binary, which never references it.

// pcmFormat describes the frames a capture client hands over.
type pcmFormat struct {
	rate     int
	channels int
	bits     int  // bits per sample: 16 or 32
	isFloat  bool // 32-bit samples may be IEEE float or integer
}

func (f pcmFormat) bytesPerFrame() int { return f.channels * f.bits / 8 }

func (f pcmFormat) valid() error {
	if f.rate <= 0 || f.channels <= 0 {
		return fmt.Errorf("capture format has %d Hz and %d channels", f.rate, f.channels)
	}
	switch {
	case f.bits == 16 && !f.isFloat:
	case f.bits == 32 && f.isFloat:
	case f.bits == 32 && !f.isFloat:
	default:
		kind := "integer"
		if f.isFloat {
			kind = "float"
		}
		return fmt.Errorf("unsupported capture format: %d-bit %s", f.bits, kind)
	}
	return nil
}

// converter downmixes to mono and resamples to SampleRate.
//
// Resampling averages every source frame that falls inside an output sample's
// window rather than picking one and dropping the rest. Plain decimation would
// fold everything above 8 kHz back down into the speech band as aliasing, and
// what the model then hears is not what was said. A box average is a crude
// low-pass, but it is the difference between attenuating that content and
// spreading it across the transcript.
//
// The window position carries across calls, so a recording split into
// hundreds of capture buffers converts identically to the same audio handed
// over in one piece. Without that, every buffer boundary would be a
// discontinuity.
type converter struct {
	format pcmFormat

	// framesPerSample is how many source frames make up one output sample.
	// It is rarely an integer: 44100 Hz gives 2.75625.
	framesPerSample float64

	// need counts down the source frames left before the current output
	// sample is complete; sum and count accumulate the window meanwhile.
	need  float64
	sum   float64
	count int

	// identity is the case where the device already gives us exactly what we
	// want, which is what AUTOCONVERT asks for and normally gets.
	identity bool
}

func newConverter(f pcmFormat) (*converter, error) {
	if err := f.valid(); err != nil {
		return nil, err
	}
	c := &converter{
		format:          f,
		framesPerSample: float64(f.rate) / float64(SampleRate),
		identity:        f.rate == SampleRate && f.channels == channels && f.bits == 16 && !f.isFloat,
	}
	c.need = c.framesPerSample
	return c, nil
}

// Convert appends the converted samples to out and returns it. raw is
// interleaved frames in the format the converter was built for; a trailing
// partial frame is ignored.
func (c *converter) Convert(raw []byte, out []int16) []int16 {
	stride := c.format.bytesPerFrame()
	if stride == 0 {
		return out
	}
	n := len(raw) / stride

	if c.identity {
		for i := 0; i < n; i++ {
			out = append(out, int16(binary.LittleEndian.Uint16(raw[i*2:])))
		}
		return out
	}

	for i := 0; i < n; i++ {
		frame := raw[i*stride : (i+1)*stride]

		// Downmix: the average of the channels, not their sum, so a stereo
		// mic does not arrive twice as loud as a mono one.
		var mono float64
		for ch := 0; ch < c.format.channels; ch++ {
			mono += c.sample(frame, ch)
		}
		mono /= float64(c.format.channels)

		c.sum += mono
		c.count++
		c.need--
		for c.need <= 0 {
			if c.count > 0 {
				out = append(out, toInt16(c.sum/float64(c.count)))
			}
			c.sum, c.count = 0, 0
			c.need += c.framesPerSample
		}
	}
	return out
}

// sample reads one channel out of an interleaved frame, normalised to ±1.
func (c *converter) sample(frame []byte, ch int) float64 {
	switch {
	case c.format.bits == 16:
		v := int16(binary.LittleEndian.Uint16(frame[ch*2:]))
		return float64(v) / 32768.0
	case c.format.isFloat:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(frame[ch*4:])))
	default:
		v := int32(binary.LittleEndian.Uint32(frame[ch*4:]))
		return float64(v) / 2147483648.0
	}
}

// toInt16 scales a ±1 sample and clamps it. A float mix bus is allowed to
// exceed ±1, and wrapping that around would turn a loud syllable into a burst
// of noise.
func toInt16(v float64) int16 {
	s := v * 32767.0
	if s > 32767 {
		return 32767
	}
	if s < -32768 {
		return -32768
	}
	return int16(math.Round(s))
}
