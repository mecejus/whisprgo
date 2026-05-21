package audio

/*
#cgo LDFLAGS: -framework AudioToolbox -framework AudioUnit -framework CoreAudio -framework CoreFoundation

#include <stdlib.h>
#include <string.h>
#include <stdatomic.h>
#include <AudioToolbox/AudioToolbox.h>
#include <AudioUnit/AudioUnit.h>
#include <CoreAudio/CoreAudio.h>
#include <CoreFoundation/CoreFoundation.h>

#define WHISPR_MAX_CLIPS    4
#define WHISPR_SAMPLE_RATE  44100
#define WHISPR_CHANNELS     2

typedef struct {
    float *samples;     // interleaved stereo float32 at WHISPR_SAMPLE_RATE
    int    frameCount;
} whispr_clip;

typedef struct {
    AudioUnit   unit;
    whispr_clip clips[WHISPR_MAX_CLIPS];

    // Mutated from Go (main thread) and read from the CoreAudio render thread.
    // -1 in currentID means "output silence".
    _Atomic int currentID;
    _Atomic int currentFrame;
} whispr_player;

static OSStatus whispr_render_cb(
    void *userData,
    AudioUnitRenderActionFlags *actionFlags,
    const AudioTimeStamp *ts,
    UInt32 busNumber,
    UInt32 numFrames,
    AudioBufferList *ioData
) {
    (void)ts; (void)busNumber;
    whispr_player *p = (whispr_player *)userData;
    float *out = (float *)ioData->mBuffers[0].mData;
    int outFrames = (int)numFrames;
    size_t silenceBytes = (size_t)outFrames * WHISPR_CHANNELS * sizeof(float);

    int id = atomic_load_explicit(&p->currentID, memory_order_acquire);
    if (id < 0 || id >= WHISPR_MAX_CLIPS || p->clips[id].samples == NULL) {
        memset(out, 0, silenceBytes);
        *actionFlags |= kAudioUnitRenderAction_OutputIsSilence;
        return noErr;
    }

    whispr_clip *clip = &p->clips[id];
    int frame = atomic_load_explicit(&p->currentFrame, memory_order_relaxed);
    int remaining = clip->frameCount - frame;
    if (remaining < 0) remaining = 0;
    int copy = remaining < outFrames ? remaining : outFrames;

    if (copy > 0) {
        memcpy(out,
               clip->samples + (size_t)frame * WHISPR_CHANNELS,
               (size_t)copy * WHISPR_CHANNELS * sizeof(float));
    }
    if (copy < outFrames) {
        memset(out + (size_t)copy * WHISPR_CHANNELS,
               0,
               (size_t)(outFrames - copy) * WHISPR_CHANNELS * sizeof(float));
    }

    int newFrame = frame + copy;
    if (newFrame >= clip->frameCount) {
        atomic_store_explicit(&p->currentID, -1, memory_order_release);
        atomic_store_explicit(&p->currentFrame, 0, memory_order_relaxed);
    } else {
        atomic_store_explicit(&p->currentFrame, newFrame, memory_order_relaxed);
    }

    return noErr;
}

static whispr_player *whispr_player_new(void) {
    whispr_player *p = (whispr_player *)calloc(1, sizeof(whispr_player));
    if (!p) return NULL;
    atomic_store_explicit(&p->currentID, -1, memory_order_relaxed);
    atomic_store_explicit(&p->currentFrame, 0, memory_order_relaxed);
    return p;
}

static int whispr_player_start(whispr_player *p) {
    AudioComponentDescription desc = {0};
    desc.componentType         = kAudioUnitType_Output;
    desc.componentSubType      = kAudioUnitSubType_DefaultOutput;
    desc.componentManufacturer = kAudioUnitManufacturer_Apple;

    AudioComponent comp = AudioComponentFindNext(NULL, &desc);
    if (!comp) return -1;

    OSStatus st = AudioComponentInstanceNew(comp, &p->unit);
    if (st != noErr) return (int)st;

    AudioStreamBasicDescription format = {0};
    format.mSampleRate       = WHISPR_SAMPLE_RATE;
    format.mFormatID         = kAudioFormatLinearPCM;
    format.mFormatFlags      = kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked;
    format.mFramesPerPacket  = 1;
    format.mChannelsPerFrame = WHISPR_CHANNELS;
    format.mBitsPerChannel   = 32;
    format.mBytesPerPacket   = sizeof(float) * WHISPR_CHANNELS;
    format.mBytesPerFrame    = sizeof(float) * WHISPR_CHANNELS;

    st = AudioUnitSetProperty(p->unit,
                              kAudioUnitProperty_StreamFormat,
                              kAudioUnitScope_Input,
                              0, &format, sizeof(format));
    if (st != noErr) return (int)st;

    AURenderCallbackStruct cb = { whispr_render_cb, p };
    st = AudioUnitSetProperty(p->unit,
                              kAudioUnitProperty_SetRenderCallback,
                              kAudioUnitScope_Input,
                              0, &cb, sizeof(cb));
    if (st != noErr) return (int)st;

    st = AudioUnitInitialize(p->unit);
    if (st != noErr) return (int)st;

    st = AudioOutputUnitStart(p->unit);
    if (st != noErr) return (int)st;

    return 0;
}

static int whispr_player_load(whispr_player *p, const char *path, int id) {
    if (id < 0 || id >= WHISPR_MAX_CLIPS) return -1;

    CFStringRef cfPath = CFStringCreateWithCString(NULL, path, kCFStringEncodingUTF8);
    if (!cfPath) return -2;
    CFURLRef url = CFURLCreateWithFileSystemPath(NULL, cfPath, kCFURLPOSIXPathStyle, false);
    CFRelease(cfPath);
    if (!url) return -3;

    ExtAudioFileRef ef = NULL;
    OSStatus st = ExtAudioFileOpenURL(url, &ef);
    CFRelease(url);
    if (st != noErr) return (int)st;

    AudioStreamBasicDescription sourceFormat = {0};
    UInt32 propSize = sizeof(sourceFormat);
    st = ExtAudioFileGetProperty(ef, kExtAudioFileProperty_FileDataFormat, &propSize, &sourceFormat);
    if (st != noErr) { ExtAudioFileDispose(ef); return (int)st; }

    AudioStreamBasicDescription clientFormat = {0};
    clientFormat.mSampleRate       = WHISPR_SAMPLE_RATE;
    clientFormat.mFormatID         = kAudioFormatLinearPCM;
    clientFormat.mFormatFlags      = kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked;
    clientFormat.mFramesPerPacket  = 1;
    clientFormat.mChannelsPerFrame = WHISPR_CHANNELS;
    clientFormat.mBitsPerChannel   = 32;
    clientFormat.mBytesPerPacket   = sizeof(float) * WHISPR_CHANNELS;
    clientFormat.mBytesPerFrame    = sizeof(float) * WHISPR_CHANNELS;

    st = ExtAudioFileSetProperty(ef,
                                 kExtAudioFileProperty_ClientDataFormat,
                                 sizeof(clientFormat), &clientFormat);
    if (st != noErr) { ExtAudioFileDispose(ef); return (int)st; }

    SInt64 sourceFrames = 0;
    propSize = sizeof(sourceFrames);
    st = ExtAudioFileGetProperty(ef, kExtAudioFileProperty_FileLengthFrames, &propSize, &sourceFrames);
    if (st != noErr) { ExtAudioFileDispose(ef); return (int)st; }

    // Allocate generously: convert source frames to client rate and add a
    // second of slack. System chimes are < 1s, so the absolute size is tiny.
    double ratio = (double)WHISPR_SAMPLE_RATE / sourceFormat.mSampleRate;
    SInt64 capacity = (SInt64)((double)sourceFrames * ratio) + WHISPR_SAMPLE_RATE;
    if (capacity < 1) capacity = WHISPR_SAMPLE_RATE;

    float *samples = (float *)calloc((size_t)capacity * WHISPR_CHANNELS, sizeof(float));
    if (!samples) { ExtAudioFileDispose(ef); return -4; }

    SInt64 readFrames = 0;
    while (readFrames < capacity) {
        UInt32 framesToRead = (UInt32)(capacity - readFrames);
        AudioBufferList bufList;
        bufList.mNumberBuffers = 1;
        bufList.mBuffers[0].mNumberChannels = WHISPR_CHANNELS;
        bufList.mBuffers[0].mDataByteSize   = framesToRead * WHISPR_CHANNELS * sizeof(float);
        bufList.mBuffers[0].mData           = samples + readFrames * WHISPR_CHANNELS;

        st = ExtAudioFileRead(ef, &framesToRead, &bufList);
        if (st != noErr) { free(samples); ExtAudioFileDispose(ef); return (int)st; }
        if (framesToRead == 0) break;
        readFrames += framesToRead;
    }
    ExtAudioFileDispose(ef);

    // Swap in the new clip. The render thread reads currentID atomically; the
    // clips array is only mutated when no clip with this id is playing, which
    // is guaranteed at startup (the only time Load is called).
    if (p->clips[id].samples) free(p->clips[id].samples);
    p->clips[id].samples    = samples;
    p->clips[id].frameCount = (int)readFrames;
    return 0;
}

static void whispr_player_play(whispr_player *p, int id) {
    if (id < 0 || id >= WHISPR_MAX_CLIPS) return;
    if (p->clips[id].samples == NULL) return;
    // Park the render thread on silence, reset the cursor, then arm the new
    // clip. The brief silence window (one render quantum, ~6 ms) avoids
    // a torn read where the render thread sees the new id with an old frame.
    atomic_store_explicit(&p->currentID, -1, memory_order_release);
    atomic_store_explicit(&p->currentFrame, 0, memory_order_relaxed);
    atomic_store_explicit(&p->currentID, id, memory_order_release);
}

static void whispr_player_close(whispr_player *p) {
    if (!p) return;
    if (p->unit) {
        AudioOutputUnitStop(p->unit);
        AudioUnitUninitialize(p->unit);
        AudioComponentInstanceDispose(p->unit);
        p->unit = NULL;
    }
    for (int i = 0; i < WHISPR_MAX_CLIPS; i++) {
        if (p->clips[i].samples) {
            free(p->clips[i].samples);
            p->clips[i].samples = NULL;
        }
    }
    free(p);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Player holds a persistent CoreAudio output stream with pre-decoded chime
// clips. Keeping the output unit running prevents the audio hardware (and
// Bluetooth audio links) from suspending, so Play returns the first sample
// to the speaker within one render quantum (~5–10 ms) instead of waiting on
// the OS to spin everything back up.
type Player struct {
	state *C.whispr_player
}

func NewPlayer() (*Player, error) {
	state := C.whispr_player_new()
	if state == nil {
		return nil, fmt.Errorf("player alloc failed")
	}
	if rc := C.whispr_player_start(state); rc != 0 {
		C.whispr_player_close(state)
		return nil, fmt.Errorf("output unit start: OSStatus %d", int(rc))
	}
	return &Player{state: state}, nil
}

// Load decodes the audio file at path into PCM at the player's internal
// format and stores it under id. Call at startup; subsequent Play(id) calls
// are lock-free.
func (p *Player) Load(id int, path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	if rc := C.whispr_player_load(p.state, cpath, C.int(id)); rc != 0 {
		return fmt.Errorf("load %s: OSStatus %d", path, int(rc))
	}
	return nil
}

func (p *Player) Play(id int) {
	C.whispr_player_play(p.state, C.int(id))
}

func (p *Player) Close() {
	if p.state != nil {
		C.whispr_player_close(p.state)
		p.state = nil
	}
}
