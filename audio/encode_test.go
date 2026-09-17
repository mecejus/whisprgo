package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tone builds a second-per-second sine wave, which is compressible enough to
// be a fair stand-in for speech without being so trivial that a broken
// encoder would still look like it shrank the data.
func tone(seconds int) []int16 {
	n := SampleRate * seconds
	s := make([]int16, n)
	for i := range s {
		t := float64(i) / float64(SampleRate)
		v := math.Sin(2*math.Pi*220*t)*8000 + math.Sin(2*math.Pi*440*t)*4000
		s[i] = int16(v)
	}
	return s
}

func TestEncodeWAVHeader(t *testing.T) {
	samples := tone(1)
	got := EncodeWAV(samples)

	if want := 44 + len(samples)*2; len(got) != want {
		t.Fatalf("WAV length = %d, want %d", len(got), want)
	}
	if string(got[0:4]) != "RIFF" || string(got[8:12]) != "WAVE" {
		t.Fatalf("bad RIFF/WAVE magic: %q %q", got[0:4], got[8:12])
	}
	if rate := binary.LittleEndian.Uint32(got[24:28]); rate != SampleRate {
		t.Errorf("sample rate = %d, want %d", rate, SampleRate)
	}
	if ch := binary.LittleEndian.Uint16(got[22:24]); ch != channels {
		t.Errorf("channels = %d, want %d", ch, channels)
	}
}

func TestEncodeFLACIsSmallerAndWellFormed(t *testing.T) {
	samples := tone(3)

	flac, err := EncodeFLAC(samples)
	if err != nil {
		t.Fatalf("EncodeFLAC: %v", err)
	}
	if !bytes.HasPrefix(flac, []byte("fLaC")) {
		t.Fatalf("output is not FLAC, first bytes: %x", flac[:min(8, len(flac))])
	}

	wav := len(EncodeWAV(samples))
	if len(flac) >= wav {
		t.Fatalf("FLAC (%d bytes) is not smaller than WAV (%d bytes)", len(flac), wav)
	}
	t.Logf("%d samples: WAV %d bytes -> FLAC %d bytes (%.0f%%)",
		len(samples), wav, len(flac), 100*float64(len(flac))/float64(wav))
}

// The whole reason for choosing FLAC over a lossy codec is that transcription
// accuracy is untouched. Decode what we produced and prove the samples survive
// the round trip exactly.
func TestEncodeFLACIsLossless(t *testing.T) {
	if _, err := exec.LookPath("afconvert"); err != nil {
		t.Skip("afconvert not available")
	}
	samples := tone(2)

	flac, err := EncodeFLAC(samples)
	if err != nil {
		t.Fatalf("EncodeFLAC: %v", err)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "a.flac")
	dst := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(src, flac, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("afconvert: %v\n%s", err, out)
	}

	decoded, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := pcmFromWAV(decoded)
	if err != nil {
		t.Fatalf("decoded WAV: %v", err)
	}
	if len(pcm) != len(samples) {
		t.Fatalf("decoded %d samples, want %d", len(pcm), len(samples))
	}
	for i := range samples {
		if pcm[i] != samples[i] {
			t.Fatalf("sample %d changed: got %d, want %d", i, pcm[i], samples[i])
		}
	}
}

func TestEncodePrefersFLAC(t *testing.T) {
	data, name := Encode(tone(2))
	if name != "audio.flac" {
		t.Errorf("filename = %q, want audio.flac", name)
	}
	if !bytes.HasPrefix(data, []byte("fLaC")) {
		t.Errorf("payload is not FLAC")
	}
}

func TestEncodeFLACRejectsEmpty(t *testing.T) {
	if _, err := EncodeFLAC(nil); err == nil {
		t.Error("expected an error for empty input")
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
		off = body + size
		if size%2 == 1 {
			off++ // chunks are word-aligned
		}
	}
	return nil, os.ErrNotExist
}
