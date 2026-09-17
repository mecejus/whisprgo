package keyboard

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <ApplicationServices/ApplicationServices.h>

void goFnState(int pressed);

static int fnWasDown = 0;
static CFMachPortRef gTap = NULL;

// Keycode 63 is the fn/Globe physical key on all Mac keyboards.
#define WHISPR_FN_KEYCODE 63

static void whispr_set_fn(int fnDown) {
    if (fnDown == fnWasDown) return;
    fnWasDown = fnDown;
    goFnState(fnDown);
}

static CGEventRef tapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
    // The system delivers these two regardless of the event mask, and disables
    // the tap when it does. Without re-enabling, the hotkey silently dies for
    // the rest of the process's life. A timeout means our callback ran long —
    // it no longer can (goFnState only hands off to a channel), but a busy
    // machine can still trip it.
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (gTap) CGEventTapEnable(gTap, true);
        // Events during the outage are lost, so fn may be up while we still
        // think it is down. Resync from live modifier state instead of waiting
        // for an edge that already passed and would leave us recording forever.
        if (fnWasDown) {
            CGEventFlags flags = CGEventSourceFlagsState(kCGEventSourceStateCombinedSessionState);
            if ((flags & kCGEventFlagMaskSecondaryFn) == 0) whispr_set_fn(0);
        }
        return event;
    }

    if (type == kCGEventFlagsChanged) {
        CGEventFlags flags = CGEventGetFlags(event);
        whispr_set_fn((flags & kCGEventFlagMaskSecondaryFn) != 0);
    } else if (type == kCGEventKeyDown || type == kCGEventKeyUp) {
        CGKeyCode kc = (CGKeyCode)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
        if (kc == WHISPR_FN_KEYCODE) whispr_set_fn(type == kCGEventKeyDown ? 1 : 0);
    }
    return event;
}

static int hasAccess() {
    return AXIsProcessTrusted() ? 1 : 0;
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

    gTap = CGEventTapCreate(
        kCGSessionEventTap,
        kCGHeadInsertEventTap,
        kCGEventTapOptionListenOnly,
        mask,
        tapCallback,
        NULL
    );
    if (!gTap) return;

    CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, gTap, 0);
    CFRunLoopAddSource(CFRunLoopGetCurrent(), src, kCFRunLoopCommonModes);
    CGEventTapEnable(gTap, true);
    CFRunLoopRun();
}
*/
import "C"
import (
	"fmt"
	"runtime"
)

// events carries fn transitions from the event-tap thread to the dispatch
// goroutine. Buffered so the tap thread never waits on a consumer: anything
// it does between receiving an event and returning is time the system counts
// against the tap's timeout, and a tap that times out is disabled.
var events = make(chan bool, 64)

//export goFnState
func goFnState(pressed C.int) {
	select {
	case events <- pressed != 0:
	default:
		// Unreachable in practice — the consumer only starts and stops the
		// recorder, which takes microseconds. Dropping beats blocking here.
	}
}

// HasAccess returns true if Accessibility access has been granted.
// It does not trigger a permission prompt.
func HasAccess() bool {
	return C.hasAccess() != 0
}

// PromptForAccess triggers the macOS Accessibility permission dialog if access
// has not been granted yet. The dialog deeplinks the user to the correct
// System Settings pane. Returns true if access is already granted.
func PromptForAccess() bool {
	return C.promptForAccess() != 0
}

// Start registers fn-key callbacks and begins listening. onStart fires the
// moment fn is pressed; onEnd fires when it's released.
//
// Both run on a dedicated goroutine, never on the event-tap thread, and must
// still return promptly: they are serialized with each other, so slow work
// (network, subprocesses, modal dialogs) belongs on a worker behind them.
//
// Returns an error if Accessibility access has not been granted.
func Start(start, end func()) error {
	if C.hasAccess() == 0 {
		return fmt.Errorf("accessibility access required")
	}

	go func() {
		for pressed := range events {
			if pressed {
				start()
			} else {
				end()
			}
		}
	}()

	go func() {
		runtime.LockOSThread()
		C.runTap()
	}()

	return nil
}
