package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/gordonklaus/portaudio"
)

const (
	sampleRate      = 16000
	channels        = 1
	framesPerBuffer = 512
)

type Recorder struct {
	mu      sync.Mutex
	samples []int16
	stream  *portaudio.Stream
}

func New() (*Recorder, error) {
	if err := portaudio.Initialize(); err != nil {
		return nil, fmt.Errorf("portaudio init: %w", err)
	}
	return &Recorder{}, nil
}

func (r *Recorder) Close() {
	portaudio.Terminate()
}

func (r *Recorder) Start() error {
	r.mu.Lock()
	r.samples = nil
	r.mu.Unlock()

	stream, err := portaudio.OpenDefaultStream(channels, 0, float64(sampleRate), framesPerBuffer, func(in []int16) {
		tmp := make([]int16, len(in))
		copy(tmp, in)
		r.mu.Lock()
		r.samples = append(r.samples, tmp...)
		r.mu.Unlock()
	})
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	if err := stream.Start(); err != nil {
		stream.Close()
		return fmt.Errorf("start stream: %w", err)
	}

	r.stream = stream
	return nil
}

func (r *Recorder) Stop() ([]byte, error) {
	if r.stream != nil {
		// Pa_StopStream guarantees the callback is not running and will not
		// be called again after this returns, so r.samples is safe to read.
		r.stream.Stop()
		r.stream.Close()
		r.stream = nil
	}

	r.mu.Lock()
	samples := make([]int16, len(r.samples))
	copy(samples, r.samples)
	r.mu.Unlock()

	return encodeWAV(samples), nil
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
