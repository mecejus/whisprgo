package paste

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// Post a Cmd+V keystroke (down then up) to paste pasteboard contents into
// the focused field. Returns 0 if the events could not be created, 1
// otherwise.
static int postCmdV(void) {
    CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
    // ANSI virtual keycode 9 = V.
    CGEventRef down = CGEventCreateKeyboardEvent(src, (CGKeyCode)9, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(src, (CGKeyCode)9, false);
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
// transcripts. Setting LANG explicitly makes the encoding deterministic
// regardless of how the process was started.
var utf8Env = append(os.Environ(), "LANG=en_US.UTF-8", "LC_CTYPE=UTF-8")

// Paste writes text to the system pasteboard and issues Cmd+V to paste it
// into the focused field. A trailing space is appended so consecutive
// dictations don't run together. The prior pasteboard contents are restored
// after the paste settles, so the clipboard isn't left clobbered. Requires
// Accessibility access.
func Paste(text string) error {
	if text == "" {
		return nil
	}

	original, hadOriginal := readPasteboard()

	if err := writePasteboard(text + " "); err != nil {
		return err
	}

	if C.postCmdV() == 0 {
		if hadOriginal {
			_ = writePasteboard(original)
		} else {
			_ = writePasteboard("")
		}
		return fmt.Errorf("could not post Cmd+V keystroke")
	}

	// Cmd+V is asynchronous — the focused app reads the pasteboard on its
	// own schedule. Wait briefly before restoring so we don't overwrite the
	// text before it's consumed.
	time.Sleep(150 * time.Millisecond)
	if hadOriginal {
		_ = writePasteboard(original)
	} else {
		_ = writePasteboard("")
	}

	return nil
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
