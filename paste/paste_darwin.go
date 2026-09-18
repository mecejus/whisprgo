package paste

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework CoreFoundation

#import <AppKit/AppKit.h>
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

// The pasteboard is driven through NSPasteboard directly rather than by
// spawning pbcopy/pbpaste. Each of those is a fork, exec and dyld load of
// AppKit — tens of milliseconds apiece, and two of them sat between the
// transcript arriving and Cmd+V going out. These calls take microseconds.
//
// NSString is UTF-16 internally and -UTF8String / -stringWithUTF8String:
// convert losslessly, so the locale dance pbcopy needed is not needed here.

// Returns the pasteboard's plain-text item as a malloc'd UTF-8 string, or
// NULL if it holds none.
static char *whispr_pb_read(void) {
    @autoreleasepool {
        NSPasteboard *pb = [NSPasteboard generalPasteboard];
        NSString *s = [pb stringForType:NSPasteboardTypeString];
        if (s == nil) return NULL;
        const char *utf8 = [s UTF8String];
        return utf8 ? strdup(utf8) : NULL;
    }
}

// Replaces the pasteboard contents with one plain-text item. Returns 1 on
// success.
static int whispr_pb_write(const char *utf8) {
    @autoreleasepool {
        NSPasteboard *pb = [NSPasteboard generalPasteboard];
        [pb clearContents];
        NSString *s = [NSString stringWithUTF8String:utf8];
        if (s == nil) return 0;
        return [pb setString:s forType:NSPasteboardTypeString] ? 1 : 0;
    }
}

static void whispr_pb_clear(void) {
    @autoreleasepool {
        [[NSPasteboard generalPasteboard] clearContents];
    }
}

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
	"time"
	"unsafe"
)

// Paste writes text to the system pasteboard and issues Cmd+V to paste it
// into the focused field. A trailing space is appended so consecutive
// dictations don't run together. The prior pasteboard text is restored
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
		restore(original, hadOriginal)
		return fmt.Errorf("could not post Cmd+V keystroke")
	}

	// Cmd+V is asynchronous — the focused app reads the pasteboard on its
	// own schedule. Wait briefly before restoring so we don't overwrite the
	// text before it's consumed.
	time.Sleep(150 * time.Millisecond)
	restore(original, hadOriginal)
	return nil
}

func restore(original string, hadOriginal bool) {
	if hadOriginal {
		_ = writePasteboard(original)
	} else {
		C.whispr_pb_clear()
	}
}

func readPasteboard() (string, bool) {
	p := C.whispr_pb_read()
	if p == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p), true
}

func writePasteboard(s string) error {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	if C.whispr_pb_write(cs) == 0 {
		return fmt.Errorf("could not write to the pasteboard")
	}
	return nil
}
