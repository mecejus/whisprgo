package audio

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreFoundation

#include <stdlib.h>
#include <AudioToolbox/AudioToolbox.h>

#define WHISPR_NUM_BUFFERS 3
#define WHISPR_BUFFER_BYTES 4096

extern void whisprAudioCallback(void *samples, int byteCount);

typedef struct {
    AudioQueueRef       queue;
    AudioQueueBufferRef buffers[WHISPR_NUM_BUFFERS];
    int                 running;
} whispr_recorder;

static void whispr_input_cb(void *userData,
                            AudioQueueRef queue,
                            AudioQueueBufferRef buffer,
                            const AudioTimeStamp *startTime,
                            UInt32 numPackets,
                            const AudioStreamPacketDescription *packetDescs) {
    whispr_recorder *r = (whispr_recorder *)userData;
    if (buffer->mAudioDataByteSize > 0) {
        whisprAudioCallback(buffer->mAudioData, (int)buffer->mAudioDataByteSize);
    }
    if (r->running) {
        AudioQueueEnqueueBuffer(queue, buffer, 0, NULL);
    }
}

static whispr_recorder *whispr_recorder_new(void) {
    return (whispr_recorder *)calloc(1, sizeof(whispr_recorder));
}

// Create the input queue and its buffers. This is the slow half of recording
// setup — it negotiates a format with the input device and, on first call,
// triggers the microphone permission check. Doing it once up front keeps it
// off the path between the fn key going down and the first captured sample.
// It does NOT light the microphone indicator; only AudioQueueStart does.
static int whispr_recorder_prime(whispr_recorder *r) {
    if (r->queue) return 0;

    AudioStreamBasicDescription format = {0};
    format.mSampleRate       = 16000.0;
    format.mFormatID         = kAudioFormatLinearPCM;
    format.mFormatFlags      = kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked;
    format.mFramesPerPacket  = 1;
    format.mChannelsPerFrame = 1;
    format.mBitsPerChannel   = 16;
    format.mBytesPerPacket   = 2;
    format.mBytesPerFrame    = 2;

    OSStatus st = AudioQueueNewInput(&format, whispr_input_cb, r, NULL, NULL, 0, &r->queue);
    if (st != 0) {
        r->queue = NULL;
        return (int)st;
    }
    for (int i = 0; i < WHISPR_NUM_BUFFERS; i++) {
        st = AudioQueueAllocateBuffer(r->queue, WHISPR_BUFFER_BYTES, &r->buffers[i]);
        if (st != 0) return (int)st;
    }
    return 0;
}

static int whispr_recorder_start(whispr_recorder *r) {
    int rc = whispr_recorder_prime(r);
    if (rc != 0) return rc;

    r->running = 1;
    // Buffers are owned by us here: either freshly allocated, or handed back
    // by the synchronous AudioQueueStop that ended the previous recording.
    for (int i = 0; i < WHISPR_NUM_BUFFERS; i++) {
        if (r->buffers[i]) {
            r->buffers[i]->mAudioDataByteSize = 0;
            AudioQueueEnqueueBuffer(r->queue, r->buffers[i], 0, NULL);
        }
    }
    OSStatus st = AudioQueueStart(r->queue, NULL);
    if (st != 0) {
        r->running = 0;
        return (int)st;
    }
    return 0;
}

// Stop the queue but keep it alive for the next recording. The synchronous
// stop flushes the partially filled buffer through the input callback first,
// so the tail of the recording isn't lost, and returns every buffer to us.
static void whispr_recorder_stop(whispr_recorder *r) {
    if (!r->queue) return;
    r->running = 0;
    AudioQueueStop(r->queue, true);
}

static void whispr_recorder_dispose(whispr_recorder *r) {
    if (!r) return;
    if (r->queue) {
        r->running = 0;
        AudioQueueStop(r->queue, true);
        for (int i = 0; i < WHISPR_NUM_BUFFERS; i++) {
            if (r->buffers[i]) {
                AudioQueueFreeBuffer(r->queue, r->buffers[i]);
                r->buffers[i] = NULL;
            }
        }
        AudioQueueDispose(r->queue, true);
        r->queue = NULL;
    }
    free(r);
}
*/
import "C"

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

// active is the capture the audio thread is currently filling, or nil
// between recordings.
var active atomic.Pointer[Capture]

type Recorder struct {
	state *C.whispr_recorder
}

func New() (*Recorder, error) {
	state := C.whispr_recorder_new()
	if state == nil {
		return nil, fmt.Errorf("recorder alloc failed")
	}
	return &Recorder{state: state}, nil
}

// Prime performs the expensive part of audio setup ahead of time. Calling it
// at startup makes the first Start as fast as every later one. Safe to skip —
// Start primes on demand — and safe to call more than once.
func (r *Recorder) Prime() error {
	if rc := C.whispr_recorder_prime(r.state); rc != 0 {
		return fmt.Errorf("AudioQueue init: OSStatus %d", int(rc))
	}
	return nil
}

func (r *Recorder) Close() {
	if r.state != nil {
		C.whispr_recorder_dispose(r.state)
		r.state = nil
	}
}

// Start begins capturing and returns the Capture that fills up as the user
// speaks. Consumers may read it immediately; they do not have to wait for
// Stop.
func (r *Recorder) Start() (*Capture, error) {
	c := newCapture()
	active.Store(c)
	if rc := C.whispr_recorder_start(r.state); rc != 0 {
		active.Store(nil)
		c.finish()
		return nil, fmt.Errorf("AudioQueue start: OSStatus %d", int(rc))
	}
	return c, nil
}

// Stop ends capture and marks the current Capture complete. The synchronous
// queue stop flushes the partially filled buffer through the callback first,
// so the tail of the recording lands in the Capture before it is finished.
func (r *Recorder) Stop() {
	C.whispr_recorder_stop(r.state)
	if c := active.Swap(nil); c != nil {
		c.finish()
	}
}

//export whisprAudioCallback
func whisprAudioCallback(data unsafe.Pointer, byteCount C.int) {
	c := active.Load()
	if c == nil {
		return
	}
	n := int(byteCount) / 2
	if n == 0 {
		return
	}
	// push copies out of the AudioQueue buffer; the staging slice the old
	// code built first was pure overhead on a realtime audio thread.
	c.push(unsafe.Slice((*int16)(data), n))
}
