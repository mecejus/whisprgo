//go:build windows

package keyboard

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"whisprgo/internal/win"
)

// Windows has no equivalent of the macOS Accessibility grant: a low-level
// keyboard hook needs no permission and no prompt. These three exist so the
// cross-platform startup path in main.go compiles and runs unchanged.
//
// HasAccess reporting true is what makes the whole permission block — the
// prompt, the marker file, the wait-and-restart dance launchd drives on a
// Mac — fall through untouched here.
func HasAccess() bool { return true }

// PromptForAccess is a no-op on Windows. Nothing to ask for.
func PromptForAccess() bool { return true }

// WaitForAccessChange is a no-op on Windows. HasAccess never returns false,
// so nothing calls this; it returns at once rather than burning the timeout
// if something ever does.
func WaitForAccessChange(timeout time.Duration) {}

const (
	whKeyboardLL = 13

	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105

	hcAction = 0

	// How often the watchdog checks that keys it believes are held really
	// are. See watchdog.
	resyncInterval = 400 * time.Millisecond
)

// kbdllhookstruct is the KBDLLHOOKSTRUCT the hook receives. It is owned by
// the OS; we only read it.
type kbdllhookstruct struct {
	vkCode      uint32
	scanCode    uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

// msg is MSG, laid out for 64-bit Windows (both amd64 and arm64 are LLP64
// with 8-byte pointers, so one definition covers them).
type msg struct {
	hwnd    uintptr
	message uint32
	_       uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
	_       uint32
}

var (
	setWindowsHookExW = win.Proc("user32.dll", "SetWindowsHookExW")
	callNextHookEx    = win.Proc("user32.dll", "CallNextHookEx")
	getMessageW       = win.Proc("user32.dll", "GetMessageW")
	getAsyncKeyState  = win.Proc("user32.dll", "GetAsyncKeyState")
	getModuleHandleW  = win.Proc("kernel32.dll", "GetModuleHandleW")
)

// spec is the parsed hold key. Swapped whole by Configure and only read after
// that, so the hook thread never takes a lock to consult it.
var spec atomic.Pointer[holdSpec]

func init() {
	s, err := parseHoldSpec(DefaultHoldKey, false)
	if err != nil {
		panic("whisprgo: the built-in default hold key does not parse: " + err.Error())
	}
	spec.Store(s)
}

// Configure selects the push-to-talk key and whether the hook hides it from
// the rest of the system. It must be called before Start.
//
// name is one or more key names joined by "+", such as "ctrl+win" or
// "capslock". passThrough only applies to a single-key hold key, which is
// swallowed by default so that holding it to dictate cannot disturb whatever
// is in front; a combination is never swallowed.
//
// An unrecognised name is an error and leaves the previous setting in place,
// so a typo in config.json costs the user their preference, not their hotkey.
func Configure(name string, passThrough bool) error {
	if name == "" {
		name = DefaultHoldKey
	}
	s, err := parseHoldSpec(name, passThrough)
	if err != nil {
		return err
	}
	spec.Store(s)
	return nil
}

// HoldKeyName returns the configured combination's name, for the ready line.
func HoldKeyName() string { return spec.Load().display }

// events carries those transitions. Buffered so the hook never waits on a
// consumer: Windows gives a low-level hook a few hundred milliseconds
// (LowLevelHooksTimeout) to return before it starts silently skipping it for
// later events, and a skipped key-up would leave whisprgo recording.
var events = make(chan keyChange, 64)

// hookCallback is created once: every syscall.NewCallback consumes a slot
// from a process-wide table that is never reclaimed.
var hookCallback = syscall.NewCallback(hookProc)

// hookProc runs on the hook thread for every keystroke in the session. It
// holds no state and makes no decision that needs any: it classifies the
// event, hands it to a channel and returns. Everything about whether a
// combination is complete is worked out on the goroutine behind it.
//
// lParam is typed as a pointer rather than a uintptr so no uintptr-to-pointer
// conversion is needed to read it; that conversion is the one go vet
// (correctly) objects to.
func hookProc(nCode uintptr, wParam uintptr, lParam *kbdllhookstruct) uintptr {
	// Anything other than HC_ACTION, including the negative codes, must be
	// passed straight through untouched.
	if nCode != hcAction || lParam == nil {
		return callNext(nCode, wParam, lParam)
	}

	// Our own injected keystrokes — the Ctrl+V that pastes a transcript, and
	// the keystroke that masks the Start menu. Without this the hook would
	// read the ctrl it just synthesised as the user reaching for the hold
	// key and start a recording on every paste.
	if lParam.dwExtraInfo == win.InjectedTag {
		return callNext(nCode, wParam, lParam)
	}

	s := spec.Load()
	if _, _, ok := s.matches(lParam.vkCode); !ok {
		return callNext(nCode, wParam, lParam)
	}

	var down bool
	switch wParam {
	case wmKeyDown, wmSysKeyDown:
		down = true
	case wmKeyUp, wmSysKeyUp:
		down = false
	default:
		return callNext(nCode, wParam, lParam)
	}

	select {
	case events <- keyChange{vk: lParam.vkCode, down: down}:
	default:
		// Unreachable in practice — the consumer only starts and stops the
		// recorder, which takes microseconds. Dropping beats blocking on the
		// one thread Windows is timing.
	}

	if s.swallow {
		// Non-zero hides the key: the focused application never sees it, so
		// holding it to dictate cannot disturb whatever is in front. Only
		// ever set for a single-key hold key.
		return 1
	}
	return callNext(nCode, wParam, lParam)
}

func callNext(nCode, wParam uintptr, lParam *kbdllhookstruct) uintptr {
	r, _, _ := callNextHookEx.Call(0, nCode, wParam, uintptr(unsafe.Pointer(lParam)))
	return r
}

// maskWin defeats the Start menu. Windows opens it when the Windows key is
// released without any other key having been pressed while it was down — and
// holding ctrl+win to dictate is exactly that, since ctrl goes down before
// win rather than during. One keystroke of a virtual-key code nothing maps
// makes the shell see an ordinary combination instead, so letting go to hear
// the transcript does not also throw the Start menu over it.
//
// This runs here, on the dispatch goroutine, and never inside the hook
// itself: injecting input from a low-level hook callback re-enters the same
// hook on the thread Windows is already timing.
func maskWin() {
	if err := win.SendKeys(win.Tap(vkNoName)...); err != nil {
		fmt.Printf("\r\033[KCould not suppress the Start menu: %v\n", err)
	}
}

// physicallyHeld asks the OS what is really down, rather than what our
// bookkeeping believes.
func physicallyHeld(s *holdSpec) bool {
	for p := 0; p < s.n; p++ {
		any := false
		for _, vk := range s.parts[p] {
			if r, _, _ := getAsyncKeyState.Call(uintptr(vk)); r&0x8000 != 0 {
				any = true
				break
			}
		}
		if !any {
			return false
		}
	}
	return true
}

// Start registers the hold-key callbacks and begins listening. onStart fires
// the moment the combination is complete; onEnd fires when any part of it is
// released.
//
// Both run on a dedicated goroutine, never on the hook thread, and must still
// return promptly: they are serialized with each other, so slow work
// (network, subprocesses, modal dialogs) belongs on a worker behind them.
//
// It does not return until the hook is installed, so a nil error means the
// hotkey is live.
func Start(start, end func()) error {
	// A low-level hook is bound to the thread that installs it, and Windows
	// delivers to it by running that thread's message loop. Installation and
	// the loop therefore share one locked thread, and the loop never returns.
	created := make(chan error, 1)
	go func() {
		runtime.LockOSThread()

		hmod, _, _ := getModuleHandleW.Call(0)
		hook, _, err := setWindowsHookExW.Call(whKeyboardLL, hookCallback, hmod, 0)
		if hook == 0 {
			created <- fmt.Errorf("the system refused the keyboard hook: %v", err)
			return
		}
		created <- nil

		// Windows needs this thread pumping messages to call the hook at
		// all. GetMessageW returns 0 on WM_QUIT and -1 on error; nothing
		// posts to this thread, so in practice it blocks here forever.
		var m msg
		for {
			r, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
		}
	}()
	if err := <-created; err != nil {
		return err
	}

	go dispatch(start, end)
	return nil
}

func dispatch(start, end func()) {
	var t tracker
	resync := time.NewTicker(resyncInterval)
	defer resync.Stop()

	for {
		select {
		case c := <-events:
			s := spec.Load()
			if !t.apply(s, c) {
				continue
			}
			if t.active {
				if s.masksWin {
					maskWin()
				}
				start()
			} else {
				end()
			}

		case <-resync.C:
			// A key-up can go missing: Ctrl+Win+L switches to the secure
			// desktop mid-combination, and a hook that overruns its timeout
			// is skipped for events it never learns it missed. Either way
			// the state machine would sit believing the key is still down
			// and record until the capture length cap stopped it. Ask the OS
			// what is actually held instead of waiting for an edge that has
			// already been and gone.
			if !t.active {
				continue
			}
			if physicallyHeld(spec.Load()) {
				continue
			}
			t.reset()
			end()
		}
	}
}
