package audio

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreFoundation

#include <stdlib.h>
#include <AudioToolbox/AudioToolbox.h>
#include <CoreFoundation/CoreFoundation.h>

// Encode 16-bit mono PCM to a FLAC file. ExtAudioFile wants a destination URL
// rather than a memory buffer, so the caller supplies a temp path and reads
// the result back.
static int whispr_encode_flac(const char *path, const short *samples, int frameCount, int sampleRate) {
    CFStringRef cfPath = CFStringCreateWithCString(NULL, path, kCFStringEncodingUTF8);
    if (!cfPath) return -2;
    CFURLRef url = CFURLCreateWithFileSystemPath(NULL, cfPath, kCFURLPOSIXPathStyle, false);
    CFRelease(cfPath);
    if (!url) return -3;

    AudioStreamBasicDescription fileFormat = {0};
    fileFormat.mSampleRate       = (Float64)sampleRate;
    fileFormat.mFormatID         = kAudioFormatFLAC;
    fileFormat.mChannelsPerFrame = 1;
    fileFormat.mBitsPerChannel   = 16;   // FLAC requires an explicit bit depth
    fileFormat.mFramesPerPacket  = 4096; // FLAC block size
    // Let CoreAudio fill in whatever else the encoder needs.
    UInt32 fmtSize = sizeof(fileFormat);
    AudioFormatGetProperty(kAudioFormatProperty_FormatInfo, 0, NULL, &fmtSize, &fileFormat);

    ExtAudioFileRef ef = NULL;
    OSStatus st = ExtAudioFileCreateWithURL(url, kAudioFileFLACType, &fileFormat, NULL,
                                            kAudioFileFlags_EraseFile, &ef);
    CFRelease(url);
    if (st != noErr) return (int)st;

    AudioStreamBasicDescription clientFormat = {0};
    clientFormat.mSampleRate       = (Float64)sampleRate;
    clientFormat.mFormatID         = kAudioFormatLinearPCM;
    clientFormat.mFormatFlags      = kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked;
    clientFormat.mFramesPerPacket  = 1;
    clientFormat.mChannelsPerFrame = 1;
    clientFormat.mBitsPerChannel   = 16;
    clientFormat.mBytesPerPacket   = 2;
    clientFormat.mBytesPerFrame    = 2;

    st = ExtAudioFileSetProperty(ef, kExtAudioFileProperty_ClientDataFormat,
                                 sizeof(clientFormat), &clientFormat);
    if (st != noErr) { ExtAudioFileDispose(ef); return (int)st; }

    AudioBufferList bufList;
    bufList.mNumberBuffers = 1;
    bufList.mBuffers[0].mNumberChannels = 1;
    bufList.mBuffers[0].mDataByteSize   = (UInt32)frameCount * 2;
    bufList.mBuffers[0].mData           = (void *)samples;

    st = ExtAudioFileWrite(ef, (UInt32)frameCount, &bufList);
    OSStatus dst = ExtAudioFileDispose(ef);
    if (st != noErr) return (int)st;
    if (dst != noErr) return (int)dst;
    return 0;
}
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"
)

// EncodeWAV wraps raw PCM in a WAV header. Always succeeds, always ~32 KB per
// second of audio.
func EncodeWAV(samples []int16) []byte {
	var buf bytes.Buffer
	dataSize := uint32(len(samples) * 2)
	buf.Grow(44 + int(dataSize))

	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, 36+dataSize)
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(SampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(SampleRate*channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(channels*2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, dataSize)
	binary.Write(&buf, binary.LittleEndian, samples)

	return buf.Bytes()
}

// EncodeFLAC compresses raw PCM losslessly, typically to a little under half
// the size of the equivalent WAV. Upload time dominates the wait after a long
// dictation, and FLAC is lossless, so this costs transcription accuracy
// nothing. Encoding a minute of 16 kHz audio takes single-digit milliseconds.
func EncodeFLAC(samples []int16) ([]byte, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("no samples")
	}

	f, err := os.CreateTemp("", "whisprgo-*.flac")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	rc := C.whispr_encode_flac(
		cpath,
		(*C.short)(unsafe.Pointer(&samples[0])),
		C.int(len(samples)),
		C.int(SampleRate),
	)
	if rc != 0 {
		return nil, fmt.Errorf("FLAC encode: OSStatus %d", int(rc))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("FLAC encode produced no output")
	}
	return data, nil
}

// Encode returns the smallest upload the API will accept for these samples,
// along with a filename whose extension tells the API what it is. FLAC is
// preferred; if CoreAudio's encoder is unavailable for any reason the WAV path
// still works, just with a bigger upload.
func Encode(samples []int16) (data []byte, filename string) {
	if flac, err := EncodeFLAC(samples); err == nil && len(flac) < len(samples)*2 {
		return flac, "audio.flac"
	}
	return EncodeWAV(samples), "audio.wav"
}
