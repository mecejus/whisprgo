//go:build windows

package win

import (
	"fmt"
	"runtime"
	"unsafe"
)

// Synthetic keystrokes, shared by the paste path (Ctrl+V) and the keyboard
// hook (the keystroke that masks the Start menu). Everything sent from here
// carries InjectedTag in dwExtraInfo, so whisprgo's own low-level hook
// recognises its own output and passes it straight through instead of reading
// it as the user working the hold key.

const (
	inputKeyboard  = 1
	keyEventFKeyUp = 0x0002
)

var sendInput = Proc("user32.dll", "SendInput")

// keybdInput is KEYBDINPUT and input is INPUT, laid out for 64-bit Windows.
// INPUT is a union whose largest member is MOUSEINPUT at 32 bytes, so the
// keyboard variant is padded out to match.
type keybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type input struct {
	typ uint32
	_   uint32
	ki  keybdInput
	_   [8]byte
}

// SendInput rejects the whole call if cbSize is not exactly the size it
// expects, and does so at runtime with no useful error. Catch a layout
// mistake here instead: the index is out of range unless INPUT is 40 bytes.
var _ = [1]struct{}{}[unsafe.Sizeof(input{})-40]

// KeyEvent is one half of a keystroke: a virtual-key code going down or up.
type KeyEvent struct {
	VK uint16
	Up bool
}

// Down and Up build the two halves of a keystroke.
func Down(vk uint16) KeyEvent { return KeyEvent{VK: vk} }
func Up(vk uint16) KeyEvent   { return KeyEvent{VK: vk, Up: true} }

// Tap is a key pressed and released.
func Tap(vk uint16) []KeyEvent { return []KeyEvent{Down(vk), Up(vk)} }

// SendKeys injects the events as one atomic batch. One SendInput call matters:
// the OS guarantees no other process's input is interleaved into a single
// call, so a modifier cannot be left hanging between two of our events.
func SendKeys(events ...KeyEvent) error {
	if len(events) == 0 {
		return nil
	}
	batch := make([]input, len(events))
	for i, e := range events {
		batch[i] = input{
			typ: inputKeyboard,
			ki: keybdInput{
				wVk:         e.VK,
				dwExtraInfo: InjectedTag,
			},
		}
		if e.Up {
			batch[i].ki.dwFlags = keyEventFKeyUp
		}
	}

	n, _, err := sendInput.Call(
		uintptr(len(batch)),
		uintptr(unsafe.Pointer(&batch[0])),
		unsafe.Sizeof(batch[0]),
	)
	// LazyProc.Call passes its arguments through a slice, so the compiler's
	// syscall keep-alive rule does not cover this one. Say it explicitly.
	runtime.KeepAlive(batch)

	if int(n) != len(batch) {
		return fmt.Errorf("sent %d of %d key events: %v", int(n), len(batch), err)
	}
	return nil
}
