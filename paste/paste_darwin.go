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
*/
import "C"
import (
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
