package audio

import (
	"io"
	"sync"
)

const (
	// SampleRate is the capture rate in Hz. Whisper resamples everything to
	// 16 kHz mono anyway, so capturing at exactly that avoids sending bytes
	// the model throws away.
	SampleRate = 16000
	channels   = 1

	// maxRecordSeconds caps a single dictation. Without it, one missed key-up
	// records until the process dies and then tries to upload the result.
	maxRecordSeconds = 300
	maxSamples       = SampleRate * maxRecordSeconds

	// Pre-size the capture buffer for a typical dictation so the audio
	// callback isn't growing a multi-megabyte slice mid-sentence.
	initialCapacity = SampleRate * 30
)

// Capture is one recording: the PCM captured so far, growing while the key is
// held, plus a wake-up signal so a consumer can follow along and upload the
// audio while it is still being spoken.
//
// The audio thread appends; any number of readers may call Read and Wait.
type Capture struct {
	mu      sync.Mutex
	samples []int16
	done    bool
	wake    chan struct{}
}

func newCapture() *Capture {
	return &Capture{
		samples: make([]int16, 0, initialCapacity),
		wake:    make(chan struct{}, 1),
	}
}

// push appends samples from the audio thread. Cheap and non-blocking apart
// from the mutex, which readers hold only for a slice header copy.
func (c *Capture) push(src []int16) {
	c.mu.Lock()
	if room := maxSamples - len(c.samples); room > 0 {
		if len(src) > room {
			src = src[:room]
		}
		c.samples = append(c.samples, src...)
	}
	c.mu.Unlock()
	c.signal()
}

// finish marks the recording complete: no more samples will arrive.
func (c *Capture) finish() {
	c.mu.Lock()
	c.done = true
	c.mu.Unlock()
	c.signal()
}

func (c *Capture) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Read returns every sample from index from onward, and whether the
// recording has ended. The returned slice is stable: the audio thread never
// rewrites samples already handed out, and a reallocation leaves the old
// backing array untouched.
func (c *Capture) Read(from int) (chunk []int16, done bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.samples)
	from = min(from, n)
	return c.samples[from:n:n], c.done
}

// Wake yields a value whenever samples have been appended or the recording
// has ended since the last receive. Check Read after each receive.
func (c *Capture) Wake() <-chan struct{} {
	return c.wake
}

// Wait blocks until at least n samples have been captured or the recording
// has ended, and returns the sample count and the ended flag.
func (c *Capture) Wait(n int) (count int, done bool) {
	for {
		c.mu.Lock()
		count, done = len(c.samples), c.done
		c.mu.Unlock()
		if count >= n || done {
			return count, done
		}
		<-c.wake
	}
}

// WaitDone blocks until the recording has ended.
func (c *Capture) WaitDone() {
	c.Wait(int(^uint(0) >> 1))
}

// Samples returns the whole recording. Call it after the recording has
// ended, i.e. once Wait or Read has reported done.
func (c *Capture) Samples() []int16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.samples[:len(c.samples):len(c.samples)]
}

// StreamFLAC encodes the capture to w as it is recorded, returning once the
// recording has ended and its last frame has been written. Every frame is
// handed to w the moment 256 ms of audio completes it, so an upload built on
// this finishes moments after the key is released.
func StreamFLAC(w io.Writer, c *Capture) error {
	enc := NewFLACWriter(w)
	cursor := 0
	for {
		chunk, done := c.Read(cursor)
		if len(chunk) > 0 {
			if err := enc.Write(chunk); err != nil {
				return err
			}
			cursor += len(chunk)
		}
		if done {
			return enc.Close()
		}
		<-c.Wake()
	}
}
