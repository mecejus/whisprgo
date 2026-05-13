package paste

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// Post a Cmd+<keycode> keystroke (down then up). Used to drive both Cmd+C
// (capture selection) and Cmd+V (paste). Returns 0 if the events could not
// be created, 1 otherwise.
static int postCmdKeystroke(int keycode) {
    CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
    CGEventRef down = CGEventCreateKeyboardEvent(src, (CGKeyCode)keycode, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(src, (CGKeyCode)keycode, false);
    if (!down || !up) {
        if (down) CFRelease(down);
        if (up)   CFRelease(up);
        if (src)  CFRelease(src);
        return 0;
    }
    CGEventSetFlags(down, kCGEventFlagMaskCommand);
    CGEventSetFlags(up,   kCGEventFlagMaskCommand);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    if (src) CFRelease(src);
    return 1;
}
*/
import "C"
import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// utf8Env forces pbcopy/pbpaste to interpret their stdin/stdout as UTF-8.
// pbcopy's default encoding is taken from LANG → LC_CTYPE →
// __CF_USER_TEXT_ENCODING (see pbcopy(1)); under launchd none of these are
// set, so it falls back to Mac Roman and mangles non-ASCII bytes from
// transcripts and LLM responses. Setting LANG explicitly makes the encoding
// deterministic regardless of how the process was started.
var utf8Env = append(os.Environ(), "LANG=en_US.UTF-8", "LC_CTYPE=UTF-8")

// ANSI virtual keycodes for the keystrokes we synthesise.
const (
	keyCodeC = 8
	keyCodeV = 9
)

// Paste writes text to the system pasteboard and issues Cmd+V to paste it
// into the focused field. On success the pasted text is left on the
// pasteboard. If the Cmd+V keystroke cannot be synthesised, the prior
// pasteboard contents are restored so the clipboard is never left in a
// half-written state. Requires Accessibility access.
func Paste(text string) error {
	if text == "" {
		return nil
	}

	original, hadOriginal := readPasteboard()

	if err := writePasteboard(text); err != nil {
		return err
	}

	if C.postCmdKeystroke(C.int(keyCodeV)) == 0 {
		if hadOriginal {
			_ = writePasteboard(original)
		} else {
			_ = writePasteboard("")
		}
		return fmt.Errorf("could not post Cmd+V keystroke")
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

	C.postCmdKeystroke(C.int(keyCodeC))

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
	cmd := exec.Command("pbpaste")
	cmd.Env = utf8Env
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func writePasteboard(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Env = utf8Env
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
