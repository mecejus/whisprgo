package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func floatFrames(t *testing.T, channels int, frames [][]float32) []byte {
	t.Helper()
	raw := make([]byte, 0, len(frames)*channels*4)
	for _, f := range frames {
		if len(f) != channels {
			t.Fatalf("frame has %d samples, want %d", len(f), channels)
		}
		for _, s := range f {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(s))
			raw = append(raw, b[:]...)
		}
	}
	return raw
}

func int16Frames(samples []int16) []byte {
	raw := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(raw[i*2:], uint16(s))
	}
	return raw
}

// The format AUTOCONVERT asks WASAPI for is the format we want, so that path
// must not touch the samples at all.
func TestConvertIdentityIsLossless(t *testing.T) {
	c, err := newConverter(pcmFormat{rate: SampleRate, channels: 1, bits: 16})
	if err != nil {
		t.Fatal(err)
	}
	if !c.identity {
		t.Fatal("16 kHz mono int16 should take the identity path")
	}

	want := []int16{0, 1, -1, 32767, -32768, 12345, -12345}
	got := c.Convert(int16Frames(want), nil)
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestConvertDownmixesChannels(t *testing.T) {
	// One output sample per source frame, so the only thing under test is the
	// channel downmix.
	c, err := newConverter(pcmFormat{rate: SampleRate, channels: 2, bits: 32, isFloat: true})
	if err != nil {
		t.Fatal(err)
	}
	raw := floatFrames(t, 2, [][]float32{
		{1.0, 0.0},   // -> 0.5
		{0.5, 0.5},   // -> 0.5
		{-1.0, 1.0},  // -> 0.0
		{-0.5, -0.5}, // -> -0.5
	})
	got := c.Convert(raw, nil)
	want := []int16{toInt16(0.5), toInt16(0.5), 0, toInt16(-0.5)}
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestConvertResamplesToSampleRate(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		c, err := newConverter(pcmFormat{rate: rate, channels: 1, bits: 32, isFloat: true})
		if err != nil {
			t.Fatal(err)
		}
		// One second of a steady level: the rate is what is under test, and a
		// constant survives any averaging unchanged.
		frames := make([][]float32, rate)
		for i := range frames {
			frames[i] = []float32{0.25}
		}
		got := c.Convert(floatFrames(t, 1, frames), nil)

		if diff := len(got) - SampleRate; diff < -1 || diff > 1 {
			t.Errorf("%d Hz: one second produced %d samples, want %d", rate, len(got), SampleRate)
		}
		want := toInt16(0.25)
		for i, s := range got {
			if s != want {
				t.Fatalf("%d Hz: sample %d is %d, want a steady %d", rate, i, s, want)
			}
		}
	}
}

// A recording arrives as hundreds of capture buffers whose sizes the device
// chooses. If the resampler restarted its window on each one, every boundary
// would be a glitch, so chunked input must convert exactly like whole input.
func TestConvertIsIndependentOfBufferBoundaries(t *testing.T) {
	const rate = 48000
	frames := make([][]float32, 4801)
	for i := range frames {
		// A tone, so a dropped or duplicated frame changes the result.
		frames[i] = []float32{float32(math.Sin(float64(i) * 0.05))}
	}
	raw := floatFrames(t, 1, frames)

	whole, err := newConverter(pcmFormat{rate: rate, channels: 1, bits: 32, isFloat: true})
	if err != nil {
		t.Fatal(err)
	}
	want := whole.Convert(raw, nil)

	// Sizes that are deliberately not multiples of the 3:1 decimation.
	for _, chunk := range []int{4, 28, 400, 1004} {
		split, err := newConverter(pcmFormat{rate: rate, channels: 1, bits: 32, isFloat: true})
		if err != nil {
			t.Fatal(err)
		}
		var got []int16
		for off := 0; off < len(raw); off += chunk {
			end := min(off+chunk, len(raw))
			got = split.Convert(raw[off:end], got)
		}
		if len(got) != len(want) {
			t.Fatalf("chunk %d: got %d samples, want %d", chunk, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("chunk %d: sample %d is %d, want %d", chunk, i, got[i], want[i])
			}
		}
	}
}

// A float mix bus may exceed ±1. Wrapping that would turn a loud syllable
// into a burst of noise, so it has to clamp.
func TestConvertClampsOutOfRangeFloats(t *testing.T) {
	c, err := newConverter(pcmFormat{rate: SampleRate, channels: 1, bits: 32, isFloat: true})
	if err != nil {
		t.Fatal(err)
	}
	got := c.Convert(floatFrames(t, 1, [][]float32{{4.0}, {-4.0}}), nil)
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	if got[0] != 32767 {
		t.Errorf("positive overload gave %d, want 32767", got[0])
	}
	if got[1] != -32768 {
		t.Errorf("negative overload gave %d, want -32768", got[1])
	}
}

func TestConvertRejectsUnsupportedFormats(t *testing.T) {
	for _, f := range []pcmFormat{
		{rate: 48000, channels: 2, bits: 24},
		{rate: 48000, channels: 2, bits: 16, isFloat: true},
		{rate: 0, channels: 1, bits: 16},
		{rate: 48000, channels: 0, bits: 16},
	} {
		if _, err := newConverter(f); err == nil {
			t.Errorf("%+v was accepted, want an error", f)
		}
	}
}

// A capture buffer can end mid-frame; the remainder must be ignored rather
// than read past.
func TestConvertIgnoresPartialTrailingFrame(t *testing.T) {
	c, err := newConverter(pcmFormat{rate: SampleRate, channels: 2, bits: 16})
	if err != nil {
		t.Fatal(err)
	}
	raw := int16Frames([]int16{100, 100, 200, 200})
	got := c.Convert(raw[:len(raw)-1], nil) // one byte short of two full frames
	if len(got) != 1 {
		t.Fatalf("got %d samples, want 1", len(got))
	}
}
