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

static void whispr_recorder_free(whispr_recorder *r) {
    free(r);
}

static int whispr_recorder_start(whispr_recorder *r) {
    AudioStreamBasicDescription format = {0};
    format.mSampleRate       = 16000.0;
    format.mFormatID         = kAudioFormatLinearPCM;
    format.mFormatFlags      = kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked;
    format.mFramesPerPacket  = 1;
    format.mChannelsPerFrame = 1;
    format.mBitsPerChannel   = 16;
    format.mBytesPerPacket   = 2;
    format.mBytesPerFrame    = 2;

    r->running = 1;

    OSStatus st = AudioQueueNewInput(&format, whispr_input_cb, r, NULL, NULL, 0, &r->queue);
    if (st != 0) {
        r->running = 0;
        return (int)st;
    }
    for (int i = 0; i < WHISPR_NUM_BUFFERS; i++) {
        st = AudioQueueAllocateBuffer(r->queue, WHISPR_BUFFER_BYTES, &r->buffers[i]);
        if (st != 0) {
            r->running = 0;
            return (int)st;
        }
        AudioQueueEnqueueBuffer(r->queue, r->buffers[i], 0, NULL);
    }
    st = AudioQueueStart(r->queue, NULL);
    if (st != 0) {
        r->running = 0;
        return (int)st;
    }
    return 0;
}

static void whispr_recorder_stop(whispr_recorder *r) {
    if (!r->queue) return;
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
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"unsafe"
)

const (
	sampleRate = 16000
	channels   = 1
)

var (
	activeMu sync.Mutex
	active   *Recorder
)

type Recorder struct {
	mu      sync.Mutex
	samples []int16
	state   *C.whispr_recorder
}

func New() (*Recorder, error) {
	state := C.whispr_recorder_new()
	if state == nil {
		return nil, fmt.Errorf("recorder alloc failed")
	}
	return &Recorder{state: state}, nil
}

func (r *Recorder) Close() {
	if r.state != nil {
		C.whispr_recorder_free(r.state)
		r.state = nil
	}
}

func (r *Recorder) Start() error {
	r.mu.Lock()
	r.samples = nil
	r.mu.Unlock()

	activeMu.Lock()
	active = r
	activeMu.Unlock()

	if rc := C.whispr_recorder_start(r.state); rc != 0 {
		activeMu.Lock()
		active = nil
		activeMu.Unlock()
		return fmt.Errorf("AudioQueue start: OSStatus %d", int(rc))
	}
	return nil
}

func (r *Recorder) Stop() ([]byte, error) {
	C.whispr_recorder_stop(r.state)

	activeMu.Lock()
	active = nil
	activeMu.Unlock()

	r.mu.Lock()
	samples := make([]int16, len(r.samples))
	copy(samples, r.samples)
	r.mu.Unlock()

	return encodeWAV(samples), nil
}

//export whisprAudioCallback
func whisprAudioCallback(data unsafe.Pointer, byteCount C.int) {
	activeMu.Lock()
	r := active
	activeMu.Unlock()
	if r == nil {
		return
	}
	n := int(byteCount) / 2
	if n == 0 {
		return
	}
	src := unsafe.Slice((*int16)(data), n)
	tmp := make([]int16, n)
	copy(tmp, src)
	r.mu.Lock()
	r.samples = append(r.samples, tmp...)
	r.mu.Unlock()
}

func encodeWAV(samples []int16) []byte {
	var buf bytes.Buffer
	dataSize := uint32(len(samples) * 2)

	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, 36+dataSize)
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, dataSize)
	binary.Write(&buf, binary.LittleEndian, samples)

	return buf.Bytes()
}
