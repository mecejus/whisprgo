package audio

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

func TestCaptureReadWaitAndFinish(t *testing.T) {
	c := newCapture()

	c.push([]int16{1, 2, 3})
	chunk, done := c.Read(0)
	if done || len(chunk) != 3 {
		t.Fatalf("Read(0) = %v, %v", chunk, done)
	}
	chunk, _ = c.Read(2)
	if len(chunk) != 1 || chunk[0] != 3 {
		t.Fatalf("Read(2) = %v", chunk)
	}
	if chunk, _ := c.Read(99); len(chunk) != 0 {
		t.Fatalf("Read past end = %v", chunk)
	}

	// A reader's slice must not see samples appended later, and must stay
	// valid across a reallocation.
	old, _ := c.Read(0)
	c.push(make([]int16, initialCapacity))
	if len(old) != 3 || old[2] != 3 {
		t.Fatal("earlier slice changed after append")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		n, done := c.Wait(1 << 30)
		if !done || n != 3+initialCapacity {
			t.Errorf("Wait after finish = %d, %v", n, done)
		}
	}()
	time.Sleep(10 * time.Millisecond)
	c.finish()
	wg.Wait()

	if _, done := c.Read(0); !done {
		t.Fatal("Read does not report done")
	}
	if len(c.Samples()) != 3+initialCapacity {
		t.Fatalf("Samples() has %d samples", len(c.Samples()))
	}
}

func TestCaptureCapsLength(t *testing.T) {
	c := newCapture()
	c.push(make([]int16, maxSamples-10))
	c.push(make([]int16, 100))
	if n := len(c.Samples()); n != maxSamples {
		t.Fatalf("capture holds %d samples, want cap of %d", n, maxSamples)
	}
}

// StreamFLAC must follow a live recording and produce exactly what a one-shot
// encode of the same samples produces.
func TestStreamFLACFollowsLiveCapture(t *testing.T) {
	samples := speechLike(SampleRate * 2)
	c := newCapture()

	var out bytes.Buffer
	errc := make(chan error, 1)
	go func() { errc <- StreamFLAC(&out, c) }()

	for off := 0; off < len(samples); off += 2048 {
		c.push(samples[off:min(off+2048, len(samples))])
		time.Sleep(time.Millisecond)
	}
	c.finish()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}

	var want bytes.Buffer
	w := NewFLACWriter(&want)
	if err := w.Write(samples); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want.Bytes()) {
		t.Fatal("streamed encoding differs from one-shot encoding")
	}
}
