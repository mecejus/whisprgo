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

// postCtrlV synthesises the paste keystroke. win.SendKeys tags every event
// with the whisprgo signature, so the keyboard hook recognises this ctrl as
// its own and passes it through — without that, the paste would look exactly
// like the user reaching for the hold key and would start a recording.
func postCtrlV() error {
	if err := win.SendKeys(
		win.Down(vkControl),
		win.Down(vkV),
		win.Up(vkV),
		win.Up(vkControl),
	); err != nil {
		return fmt.Errorf("could not post Ctrl+V keystroke: %w", err)
	}
	return nil
}
