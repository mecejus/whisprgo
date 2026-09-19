//go:build windows

package audio

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"whisprgo/internal/win"
)

// The WASAPI capture stream, built to the same shape as the AudioQueue one on
// macOS: expensive setup happens once in Prime, and each dictation only
// starts and stops an already-configured stream. Starting is also what
// lights Windows' microphone indicator, so a stream left running would show
// the user as permanently listening — the same reason the Mac side does not
// hold its queue open either.
//
// Everything COM touches happens on one locked OS thread. WASAPI objects are
// apartment-bound, and pinning them to a single thread is simpler to reason
// about than proving every caller is in the right apartment.

const (
	waitObject0 = 0

	// How long the capture loop waits on the buffer event before looking at
	// the recording flag again. While the stream runs the event arrives every
	// device period (~10 ms), so this only bounds how long Stop can block the
	// key-handler goroutine if an event is ever missed.
	captureWaitMillis = 20

	// Endpoint buffer length, in 100 ns units: 200 ms. The event still fires
	// every device period; this is just how much slack there is before a
	// scheduling hiccup would cost samples.
	bufferDuration = 2_000_000
)

var (
	createEventW        = win.Proc("kernel32.dll", "CreateEventW")
	closeHandle         = win.Proc("kernel32.dll", "CloseHandle")
	waitForSingleObject = win.Proc("kernel32.dll", "WaitForSingleObject")
)

type cmdKind int

const (
	cmdPrime cmdKind = iota
	cmdStart
	cmdClose
)

type command struct {
	kind    cmdKind
	capture *Capture
	reply   chan error
}

type Recorder struct {
	cmds    chan command
	stopped chan struct{}

	// recording is read by the capture loop and cleared by Stop, which is the
	// only cross-thread signal in here.
	recording atomic.Bool

	// active is the capture the worker is filling, or nil between recordings.
	active atomic.Pointer[Capture]

	// Everything below belongs to the worker thread alone.
	enum    *iMMDeviceEnumerator
	client  *iAudioClient
	capture *iAudioCaptureClient
	event   syscall.Handle
	format  pcmFormat
	conv    *converter
	primed  bool

	// Reused across capture buffers so the audio path allocates nothing.
	scratch []int16
	silence []byte
}

func New() (*Recorder, error) {
	r := &Recorder{
		cmds:    make(chan command),
		stopped: make(chan struct{}, 1),
	}
	ready := make(chan error, 1)
	go r.worker(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return r, nil
}

// Prime performs the expensive part of audio setup ahead of time — finding
// the device, negotiating a format and allocating the endpoint buffer — so
// the first Start is as fast as every later one. Safe to skip, since Start
// primes on demand, and safe to call more than once.
func (r *Recorder) Prime() error { return r.send(command{kind: cmdPrime}) }

// Start begins capturing and returns the Capture that fills up as the user
// speaks. Consumers may read it immediately; they do not have to wait for
// Stop.
func (r *Recorder) Start() (*Capture, error) {
	c := newCapture()
	if err := r.send(command{kind: cmdStart, capture: c}); err != nil {
		c.finish()
		return nil, err
	}
	return c, nil
}

// Stop ends capture and marks the current Capture complete. It waits for the
// worker to flush what the endpoint buffer still holds, so the tail of the
// recording lands in the Capture before it is finished — the same guarantee
// AudioQueueStop gives on macOS.
func (r *Recorder) Stop() {
	if !r.recording.CompareAndSwap(true, false) {
		return
	}
	<-r.stopped
}

func (r *Recorder) Close() {
	r.Stop()
	if r.cmds == nil {
		return
	}
	_ = r.send(command{kind: cmdClose})
	r.cmds = nil
}

func (r *Recorder) send(c command) error {
	c.reply = make(chan error, 1)
	r.cmds <- c
	return <-c.reply
}

func (r *Recorder) worker(ready chan error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := coInitialize(); err != nil {
		ready <- err
		return
	}
	defer coUninitialize.Call()

	enum, err := createDeviceEnumerator()
	if err != nil {
		ready <- err
		return
	}
	r.enum = enum
	ready <- nil

	for cmd := range r.cmds {
		switch cmd.kind {
		case cmdPrime:
			cmd.reply <- r.prime()

		case cmdStart:
			err := r.prime()
			if err == nil {
				err = r.begin(cmd.capture)
			}
			cmd.reply <- err
			if err == nil {
				// Blocks until Stop clears the recording flag. No commands
				// are served meanwhile, which is why Stop signals through an
				// atomic rather than through this channel.
				r.captureLoop()
			}

		case cmdClose:
			r.release()
			cmd.reply <- nil
			return
		}
	}
}

func (r *Recorder) prime() error {
	if r.primed {
		return nil
	}

	dev, err := r.enum.GetDefaultAudioEndpoint(eCapture, eConsole)
	if err != nil {
		return err
	}
	defer dev.Release()

	client, format, err := openCaptureClient(dev)
	if err != nil {
		return err
	}

	event, _, callErr := createEventW.Call(0, 0, 0, 0) // auto-reset, initially clear
	if event == 0 {
		client.Release()
		return fmt.Errorf("create the capture event: %v", callErr)
	}
	if err := client.SetEventHandle(syscall.Handle(event)); err != nil {
		closeHandle.Call(event)
		client.Release()
		return err
	}

	capture, err := client.GetCaptureClient()
	if err != nil {
		closeHandle.Call(event)
		client.Release()
		return err
	}

	r.client, r.capture, r.event, r.format = client, capture, syscall.Handle(event), format
	r.primed = true
	return nil
}

// openCaptureClient asks WASAPI for 16 kHz mono directly. Shared mode
// otherwise hands over whatever the audio engine is mixing in — usually
// 48 kHz stereo float — so without AUTOCONVERT every dictation would pay for
// a resample in our own code. If the driver will not do it, the mix format is
// taken as-is and the converter does the work instead.
func openCaptureClient(dev *iMMDevice) (*iAudioClient, pcmFormat, error) {
	want := waveFormatEx{
		wFormatTag:      waveFormatPCM,
		nChannels:       channels,
		nSamplesPerSec:  SampleRate,
		nAvgBytesPerSec: SampleRate * channels * 2,
		nBlockAlign:     channels * 2,
		wBitsPerSample:  16,
	}

	client, err := dev.ActivateAudioClient()
	if err != nil {
		return nil, pcmFormat{}, err
	}
	err = client.Initialize(
		streamflagsEventCallback|streamflagsAutoConvertPCM|streamflagsSRCDefaultQuality,
		bufferDuration, &want)
	if err == nil {
		return client, pcmFormat{rate: SampleRate, channels: channels, bits: 16}, nil
	}
	autoConvertErr := err

	// A client whose Initialize failed cannot be reinitialized, so the retry
	// needs a fresh one.
	client.Release()
	client, err = dev.ActivateAudioClient()
	if err != nil {
		return nil, pcmFormat{}, err
	}

	mix, err := client.GetMixFormat()
	if err != nil {
		client.Release()
		return nil, pcmFormat{}, err
	}
	defer coTaskMemFree.Call(uintptr(unsafe.Pointer(mix)))

	format, err := mix.describe()
	if err != nil {
		client.Release()
		return nil, pcmFormat{}, err
	}
	if err := client.Initialize(streamflagsEventCallback, bufferDuration, mix); err != nil {
		client.Release()
		return nil, pcmFormat{}, fmt.Errorf("%w (asking for 16 kHz mono first failed with: %v)", err, autoConvertErr)
	}
	return client, format, nil
}

// begin arms the stream for one dictation. The converter is rebuilt each time
// because it carries a resampling window across calls, and that window
// belongs to one recording.
func (r *Recorder) begin(c *Capture) error {
	conv, err := newConverter(r.format)
	if err != nil {
		return err
	}
	r.conv = conv
	r.active.Store(c)
	r.recording.Store(true)

	if err := r.client.Start(); err != nil {
		r.recording.Store(false)
		r.active.Store(nil)
		return err
	}
	return nil
}

func (r *Recorder) captureLoop() {
	for r.recording.Load() {
		if ret, _, _ := waitForSingleObject.Call(uintptr(r.event), captureWaitMillis); uint32(ret) == waitObject0 {
			r.drain()
		}
	}

	// Whatever the endpoint buffer still holds is the tail of the recording.
	// Take it before stopping, and finish the Capture only after it has
	// landed.
	r.drain()
	_ = r.client.Stop()
	_ = r.client.Reset()
	if c := r.active.Swap(nil); c != nil {
		c.finish()
	}
	r.stopped <- struct{}{}
}

// drain moves every packet WASAPI is holding into the active Capture. It runs
// on the worker thread between event waits, so it allocates nothing: the
// converted samples go through a scratch slice that push copies out of.
func (r *Recorder) drain() {
	stride := r.format.bytesPerFrame()
	for {
		packet, err := r.capture.GetNextPacketSize()
		if err != nil || packet == 0 {
			return
		}

		data, frames, flags, empty, err := r.capture.GetBuffer()
		if err != nil || empty {
			return
		}

		if frames > 0 {
			var raw []byte
			switch {
			case flags&bufferflagsSilent != 0:
				// The buffer's contents are undefined when SILENT is set;
				// the correct reading is that many frames of digital
				// silence, which still has to reach the recording so its
				// timing stays right.
				raw = r.silenceBytes(int(frames) * stride)
			case data != nil:
				raw = unsafe.Slice(data, int(frames)*stride)
			}

			if c := r.active.Load(); c != nil && len(raw) > 0 {
				r.scratch = r.conv.Convert(raw, r.scratch[:0])
				if len(r.scratch) > 0 {
					c.push(r.scratch)
				}
			}
		}

		if err := r.capture.ReleaseBuffer(frames); err != nil {
			return
		}
	}
}

func (r *Recorder) silenceBytes(n int) []byte {
	if cap(r.silence) < n {
		r.silence = make([]byte, n)
	}
	s := r.silence[:n]
	for i := range s {
		s[i] = 0
	}
	return s
}

func (r *Recorder) release() {
	if r.capture != nil {
		r.capture.Release()
		r.capture = nil
	}
	if r.client != nil {
		r.client.Release()
		r.client = nil
	}
	if r.event != 0 {
		closeHandle.Call(uintptr(r.event))
		r.event = 0
	}
	if r.enum != nil {
		r.enum.Release()
		r.enum = nil
	}
	r.primed = false
}
