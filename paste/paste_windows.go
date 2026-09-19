//go:build windows

package paste

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"whisprgo/internal/win"
)

// The clipboard is driven through the Win32 clipboard API directly rather
// than by shelling out to PowerShell's Set-Clipboard. Starting a PowerShell
// host is hundreds of milliseconds, and it would sit between the transcript
// arriving and Ctrl+V going out — the one stretch of the dictation path the
// user actually waits through. These calls take microseconds.
//
// Text crosses as CF_UNICODETEXT, which is UTF-16, so Go strings round-trip
// without a locale in sight.

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	inputKeyboard  = 1
	keyEventFKeyUp = 0x0002

	vkControl = 0x11
	vkV       = 0x56

	// Longest a foreign clipboard owner is given up on. The clipboard is a
	// single system-wide lock and other apps hold it briefly all the time,
	// so a first failure means "try again", not "broken".
	clipboardAttempts = 12
	clipboardBackoff  = 10 * time.Millisecond

	// A pasteboard string longer than this is not something we wrote.
	maxClipboardChars = 1 << 22
)

var (
	openClipboard    = win.Proc("user32.dll", "OpenClipboard")
	closeClipboard   = win.Proc("user32.dll", "CloseClipboard")
	emptyClipboard   = win.Proc("user32.dll", "EmptyClipboard")
	getClipboardData = win.Proc("user32.dll", "GetClipboardData")
	setClipboardData = win.Proc("user32.dll", "SetClipboardData")
	sendInput        = win.Proc("user32.dll", "SendInput")

	globalAlloc  = win.Proc("kernel32.dll", "GlobalAlloc")
	globalFree   = win.Proc("kernel32.dll", "GlobalFree")
	globalLock   = win.Proc("kernel32.dll", "GlobalLock")
	globalUnlock = win.Proc("kernel32.dll", "GlobalUnlock")
	globalSize   = win.Proc("kernel32.dll", "GlobalSize")

	// Win32 memory is copied with RtlMoveMemory rather than by reshaping the
	// locked address into a Go slice. Converting a uintptr that came back
	// from GlobalLock into an unsafe.Pointer is exactly the pattern go vet
	// rejects, and it deserves to: nothing keeps the block alive across the
	// conversion. Handing the address straight back to a syscall never
	// materialises a Go pointer to foreign memory at all.
	rtlMoveMemory = win.Proc("kernel32.dll", "RtlMoveMemory")
)

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

// Paste writes text to the clipboard and issues Ctrl+V to paste it into the
// focused field. A trailing space is appended so consecutive dictations don't
// run together. The prior clipboard text is restored after the paste settles,
// so the clipboard isn't left clobbered.
func Paste(text string) error {
	if text == "" {
		return nil
	}

	original, hadOriginal := readClipboard()

	if err := writeClipboard(text + " "); err != nil {
		return err
	}

	if err := postCtrlV(); err != nil {
		restore(original, hadOriginal)
		return err
	}

	// Ctrl+V is asynchronous — the focused app reads the clipboard on its
	// own schedule. Wait briefly before restoring so we don't overwrite the
	// text before it's consumed.
	time.Sleep(150 * time.Millisecond)
	restore(original, hadOriginal)
	return nil
}

func restore(original string, hadOriginal bool) {
	if hadOriginal {
		_ = writeClipboard(original)
		return
	}
	_ = withClipboard(func() error {
		emptyClipboard.Call()
		return nil
	})
}

// withClipboard opens the clipboard, runs fn, and closes it again, retrying
// the open while another process holds the lock.
func withClipboard(fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < clipboardAttempts; attempt++ {
		r, _, err := openClipboard.Call(0)
		if r != 0 {
			defer closeClipboard.Call()
			return fn()
		}
		lastErr = err
		time.Sleep(clipboardBackoff)
	}
	return fmt.Errorf("could not open the clipboard: %v", lastErr)
}

func readClipboard() (string, bool) {
	var out string
	var ok bool
	_ = withClipboard(func() error {
		h, _, _ := getClipboardData.Call(cfUnicodeText)
		if h == 0 {
			return nil
		}
		size, _, _ := globalSize.Call(h)
		if size < 2 {
			return nil
		}
		if size > maxClipboardChars*2 {
			size = maxClipboardChars * 2
		}

		p, _, _ := globalLock.Call(h)
		if p == 0 {
			return nil
		}
		buf := make([]uint16, size/2)
		rtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), p, size)
		runtime.KeepAlive(buf)
		globalUnlock.Call(h)

		out = win.UTF16ToString(buf)
		ok = true
		return nil
	})
	return out, ok
}

func writeClipboard(s string) error {
	u16, err := syscall.UTF16FromString(s)
	if err != nil {
		return fmt.Errorf("could not encode text for the clipboard: %w", err)
	}

	return withClipboard(func() error {
		// EmptyClipboard is what makes this process the clipboard owner,
		// which SetClipboardData requires.
		if r, _, err := emptyClipboard.Call(); r == 0 {
			return fmt.Errorf("could not empty the clipboard: %v", err)
		}

		size := uintptr(len(u16) * 2)
		h, _, err := globalAlloc.Call(gmemMoveable, size)
		if h == 0 {
			return fmt.Errorf("could not allocate clipboard memory: %v", err)
		}

		p, _, err := globalLock.Call(h)
		if p == 0 {
			globalFree.Call(h)
			return fmt.Errorf("could not lock clipboard memory: %v", err)
		}
		rtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&u16[0])), size)
		runtime.KeepAlive(u16)
		globalUnlock.Call(h)

		if r, _, err := setClipboardData.Call(cfUnicodeText, h); r == 0 {
			// Ownership only transfers to the system on success, so this
			// block is the one place the handle is still ours to free.
			globalFree.Call(h)
			return fmt.Errorf("could not write to the clipboard: %v", err)
		}
		return nil
	})
}

// postCtrlV synthesises the paste keystroke. Every event carries
// win.InjectedTag in dwExtraInfo so whisprgo's own keyboard hook recognises
// it and passes it straight through — without that, the ctrl we send here
// would look exactly like the user reaching for the hold key and would start
// a recording on every paste.
func postCtrlV() error {
	events := [...]input{
		{typ: inputKeyboard, ki: keybdInput{wVk: vkControl, dwExtraInfo: win.InjectedTag}},
		{typ: inputKeyboard, ki: keybdInput{wVk: vkV, dwExtraInfo: win.InjectedTag}},
		{typ: inputKeyboard, ki: keybdInput{wVk: vkV, dwFlags: keyEventFKeyUp, dwExtraInfo: win.InjectedTag}},
		{typ: inputKeyboard, ki: keybdInput{wVk: vkControl, dwFlags: keyEventFKeyUp, dwExtraInfo: win.InjectedTag}},
	}
	n, _, err := sendInput.Call(
		uintptr(len(events)),
		uintptr(unsafe.Pointer(&events[0])),
		unsafe.Sizeof(events[0]),
	)
	// LazyProc.Call passes its arguments through a slice, so the compiler's
	// syscall keep-alive rule does not cover this array. Say it explicitly.
	runtime.KeepAlive(&events)
	if int(n) != len(events) {
		return fmt.Errorf("could not post Ctrl+V keystroke: %v", err)
	}
	return nil
}
