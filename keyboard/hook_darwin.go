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
	"sync"
	"time"
)

// Mode identifies which interaction the user is invoking.
type Mode int

const (
	// ModeDictation is a plain hold of fn — record while held, transcribe on
	// release.
	ModeDictation Mode = iota
	// ModeAgent is a quick tap of fn followed by a second press within the
	// double-tap window — record on the second press, transcribe + LLM +
	// speak on release.
	ModeAgent
)

// tapThreshold is the maximum press duration that still counts as a "tap"
// rather than a hold. Holds shorter than this never trigger dictation; their
// release just opens the double-tap window for a possible agent press.
const tapThreshold = 250 * time.Millisecond

// doubleTapWindow is how long after a tap-release a second press can arrive
// and still count as the agent trigger.
const doubleTapWindow = 300 * time.Millisecond

type fnState int

const (
	stateIdle fnState = iota
	statePending
	stateDictation
	stateAgent
)

var (
	onStart func(Mode)
	onEnd   func(Mode)

	mu           sync.Mutex
	state        fnState
	pendingTimer *time.Timer
	tapDeadline  time.Time
)

//export goFnState
func goFnState(pressed C.int) {
	if pressed != 0 {
		handlePress()
	} else {
		handleRelease()
	}
}

func handlePress() {
	mu.Lock()

	if !tapDeadline.IsZero() && time.Now().Before(tapDeadline) {
		// Second press of a double-tap → agent mode, fire immediately.
		tapDeadline = time.Time{}
		state = stateAgent
		mu.Unlock()
		if onStart != nil {
			onStart(ModeAgent)
		}
		return
	}

	// Tentative — could be a tap (no callback) or a hold (dictation).
	// Defer firing onStart until we know it's a hold; the alternative is to
	// fire on every press and retract on tap, which would briefly play the
	// recording UI for the first tap of a double-tap.
	state = statePending
	if pendingTimer != nil {
		pendingTimer.Stop()
	}
	pendingTimer = time.AfterFunc(tapThreshold, confirmDictation)
	mu.Unlock()
}

func confirmDictation() {
	mu.Lock()
	if state != statePending {
		mu.Unlock()
		return
	}
	state = stateDictation
	mu.Unlock()
	if onStart != nil {
		onStart(ModeDictation)
	}
}

func handleRelease() {
	mu.Lock()
	switch state {
	case statePending:
		// Released before the hold threshold — a tap. Cancel the pending
		// dictation timer and open the double-tap window for a possible
		// agent press.
		if pendingTimer != nil {
			pendingTimer.Stop()
			pendingTimer = nil
		}
		state = stateIdle
		tapDeadline = time.Now().Add(doubleTapWindow)
		mu.Unlock()
	case stateDictation:
		state = stateIdle
		tapDeadline = time.Time{}
		mu.Unlock()
		if onEnd != nil {
			onEnd(ModeDictation)
		}
	case stateAgent:
		state = stateIdle
		tapDeadline = time.Time{}
		mu.Unlock()
		if onEnd != nil {
			onEnd(ModeAgent)
		}
	default:
		mu.Unlock()
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

// Start registers fn-key callbacks and begins listening. onStart fires when a
// recording session begins (either dictation, after a 250ms hold, or agent,
// immediately on the second press of a double-tap). onEnd fires when the user
// releases fn during an active session.
//
// Returns an error if Accessibility access has not been granted.
func Start(start, end func(Mode)) error {
	if C.hasAccess() == 0 {
		return fmt.Errorf("accessibility access required")
	}

	onStart = start
	onEnd = end

	go func() {
		runtime.LockOSThread()
		C.runTap()
	}()

	return nil
}
