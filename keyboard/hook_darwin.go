package keyboard

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <ApplicationServices/ApplicationServices.h>

void goFnState(int pressed);

static int fnWasDown = 0;

static CGEventRef tapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
    if (type == kCGEventFlagsChanged) {
        CGEventFlags flags = CGEventGetFlags(event);
        // 0x800000 = kCGEventFlagMaskSecondaryFn — the fn modifier bit
        int fnDown = (flags & 0x800000) != 0;
        if (fnDown != fnWasDown) {
            fnWasDown = fnDown;
            goFnState(fnDown);
        }
    } else if (type == kCGEventKeyDown || type == kCGEventKeyUp) {
        // Keycode 63 is the fn/Globe physical key on all Mac keyboards
        CGKeyCode kc = (CGKeyCode)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
        if (kc == 63) {
            int fnDown = (type == kCGEventKeyDown) ? 1 : 0;
            if (fnDown != fnWasDown) {
                fnWasDown = fnDown;
                goFnState(fnDown);
            }
        }
    }
    return event;
}

static int promptForAccess() {
    CFStringRef keys[]   = { kAXTrustedCheckOptionPrompt };
    CFBooleanRef values[] = { kCFBooleanTrue };
    CFDictionaryRef options = CFDictionaryCreate(
        NULL,
        (const void **)keys,
        (const void **)values,
        1,
        &kCFTypeDictionaryKeyCallBacks,
        &kCFTypeDictionaryValueCallBacks
    );
    Boolean trusted = AXIsProcessTrustedWithOptions(options);
    CFRelease(options);
    return trusted ? 1 : 0;
}

static void runTap() {
    CGEventMask mask = CGEventMaskBit(kCGEventFlagsChanged) |
                       CGEventMaskBit(kCGEventKeyDown)      |
                       CGEventMaskBit(kCGEventKeyUp);

    CFMachPortRef tap = CGEventTapCreate(
        kCGSessionEventTap,
        kCGHeadInsertEventTap,
        kCGEventTapOptionListenOnly,
        mask,
        tapCallback,
        NULL
    );
    if (!tap) return;

    CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0);
    CFRunLoopAddSource(CFRunLoopGetCurrent(), src, kCFRunLoopCommonModes);
    CGEventTapEnable(tap, true);
    CFRunLoopRun();
}
*/
import "C"
import (
	"fmt"
	"runtime"
)

var (
	onPress   func()
	onRelease func()
)

//export goFnState
func goFnState(pressed C.int) {
	if pressed != 0 {
		if onPress != nil {
			onPress()
		}
	} else {
		if onRelease != nil {
			onRelease()
		}
	}
}

// PromptForAccess triggers the macOS Accessibility permission dialog if access
// has not been granted yet. The dialog deeplinks the user to the correct
// System Settings pane. Returns true if access is already granted.
func PromptForAccess() bool {
	return C.promptForAccess() != 0
}

// Start registers fn-key press/release callbacks and begins listening.
// Returns an error if Accessibility access has not been granted.
func Start(press, release func()) error {
	if C.promptForAccess() == 0 {
		return fmt.Errorf("accessibility access required")
	}

	onPress = press
	onRelease = release

	go func() {
		runtime.LockOSThread()
		C.runTap()
	}()

	return nil
}
