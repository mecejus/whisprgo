package paste

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// Post a single keyboard event carrying a chunk of UTF-16 code units as the
// event's Unicode string. The keycode is ignored when a Unicode string is set;
// the system delivers the characters to the focused text field directly.
static void postUnicodeChunk(const UniChar *buf, int len) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, 0, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(NULL, 0, false);
    CGEventKeyboardSetUnicodeString(down, len, buf);
    CGEventKeyboardSetUnicodeString(up,   len, buf);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
}

// Post Cmd+C to copy the current selection in the focused app. Uses the ANSI
// virtual keycode for 'C' (8) with the kCGEventFlagMaskCommand modifier.
static void postCmdC() {
    CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
    CGEventRef down = CGEventCreateKeyboardEvent(src, (CGKeyCode)8, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(src, (CGKeyCode)8, false);
    CGEventSetFlags(down, kCGEventFlagMaskCommand);
    CGEventSetFlags(up,   kCGEventFlagMaskCommand);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    if (src) CFRelease(src);
}
*/
import "C"
import (
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"
)

// chunkSize caps how many UTF-16 code units we attach to a single event.
// CGEventKeyboardSetUnicodeString silently truncates very long strings
// (limit is somewhere around ~20 units depending on macOS version), so we
// stay well under that.
const chunkSize = 16

// Paste types text directly into the focused text field via CGEventPost,
// without touching the clipboard. Requires Accessibility access (the same
// permission already needed for the Fn-key hook).
func Paste(text string) error {
	if text == "" {
		return nil
	}
	units := utf16.Encode([]rune(text))
	for i := 0; i < len(units); i += chunkSize {
		end := i + chunkSize
		if end > len(units) {
			end = len(units)
		}
		chunk := units[i:end]
		C.postUnicodeChunk(
			(*C.UniChar)(unsafe.Pointer(&chunk[0])),
			C.int(len(chunk)),
		)
	}
	return nil
}

// selectionSentinel is written to the pasteboard before issuing Cmd+C so we
// can tell whether the keystroke actually copied anything. If the pasteboard
// still holds this exact value when we poll, the focused app had no selection
// (or doesn't respond to Cmd+C). The value is deliberately odd to avoid
// matching real clipboard content.
const selectionSentinel = "\x00whisprgo-no-selection\x00"

// CaptureSelection copies the focused app's current text selection via Cmd+C
// and returns it. The pasteboard is restored to its prior contents before
// returning. ok is false if no selection was captured (the pasteboard never
// changed from the sentinel value we wrote).
//
// Requires Accessibility access — same as Paste.
func CaptureSelection() (text string, ok bool) {
	original, hadOriginal := readPasteboard()
	defer func() {
		if hadOriginal {
			_ = writePasteboard(original)
		} else {
			_ = writePasteboard("")
		}
	}()

	if err := writePasteboard(selectionSentinel); err != nil {
		return "", false
	}

	C.postCmdC()

	// Cmd+C is asynchronous: the system delivers it to the focused app, which
	// then writes to the pasteboard on its own schedule. Poll briefly for a
	// change away from the sentinel.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
		current, _ := readPasteboard()
		if current != selectionSentinel {
			trimmed := strings.TrimSpace(current)
			if trimmed == "" {
				return "", false
			}
			return current, true
		}
	}
	return "", false
}

func readPasteboard() (string, bool) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func writePasteboard(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
