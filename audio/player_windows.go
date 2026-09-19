//go:build windows

package audio

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"whisprgo/internal/win"
)

// Chimes go through winmm's PlaySound rather than a second WASAPI render
// stream. The Mac keeps an AudioUnit running because letting the output
// device — and a Bluetooth link especially — suspend between dictations put
// a wake-up in front of the chime. PlaySound has no such stop to make: it
// hands the buffer to the mixer, which is already running for everything
// else on the machine, and returns immediately.
//
// The alternative, several hundred lines of a second COM render client, buys
// back a few milliseconds on a sound whose whole job is to say "start
// talking". It is not worth the surface area.

const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndMemory    = 0x0004
)

var playSoundW = win.Proc("winmm.dll", "PlaySoundW")

// Player holds the chime clips in memory. PlaySound reads the buffer
// asynchronously, so a clip must outlive the call that started it; keeping
// every clip for the life of the process is the simplest way to guarantee
// that.
type Player struct {
	mu    sync.RWMutex
	clips map[int][]byte
}

func NewPlayer() (*Player, error) {
	return &Player{clips: make(map[int][]byte)}, nil
}

// Load reads the WAV file at path into memory and stores it under id. Call at
// startup; Play then touches no filesystem.
func (p *Player) Load(id int, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	// PlaySound reports nothing useful when handed something it cannot
	// parse — it just plays silence. Checking the RIFF header here turns a
	// mystery into a startup error.
	if len(data) < 12 || !bytes.Equal(data[0:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		return fmt.Errorf("load %s: not a WAV file", path)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.clips[id] = data
	return nil
}

// Play starts the clip and returns at once. A chime already sounding is cut
// off, which is what should happen when dictations follow each other quickly.
func (p *Player) Play(id int) {
	p.mu.RLock()
	clip := p.clips[id]
	p.mu.RUnlock()
	if len(clip) == 0 {
		return
	}
	// clip stays referenced by p.clips for the life of the process, so the
	// buffer PlaySound keeps reading after this returns cannot be collected.
	playSoundW.Call(
		uintptr(unsafe.Pointer(&clip[0])),
		0,
		sndMemory|sndAsync|sndNoDefault,
	)
}

func (p *Player) Close() {
	// Stop whatever is playing before the buffers go away.
	playSoundW.Call(0, 0, 0)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.clips = make(map[int][]byte)
}
