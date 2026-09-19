//go:build windows

package keyboard

import (
	"fmt"
	"runtime"
	"strings"
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
	getModuleHandleW  = win.Proc("kernel32.dll", "GetModuleHandleW")
)

// events carries hold-key transitions from the hook thread to the dispatch
// goroutine. Buffered so the hook never waits on a consumer: Windows gives a
// low-level hook a few hundred milliseconds (LowLevelHooksTimeout) to return
// before it starts silently skipping it for later events, and a skipped
// key-up would leave whisprgo recording forever.
var events = make(chan bool, 64)

// holdKey is the virtual-key code of the push-to-talk key, and swallow says
// whether the hook eats it.
//
// Atomics rather than a mutex: these are read on the hook thread for every
// keystroke that happens anywhere in the session, and that thread is the one
// Windows is timing. An uncontended lock would be cheap, but nothing here
// needs the two values to change together, so there is no reason to put a
// lock on that path at all.
var (
	holdKey atomic.Uint32
	swallow atomic.Bool
)

func init() {
	holdKey.Store(vkRControl)
	swallow.Store(true)
}

// Virtual-key codes. A low-level hook reports the side-distinguished codes
// (VK_RCONTROL rather than VK_CONTROL), which is what makes a right-hand-only
// hold key possible at all.
const (
	vkLControl = 0xA2
	vkRControl = 0xA3
	vkLShift   = 0xA0
	vkRShift   = 0xA1
	vkLMenu    = 0xA4
	vkRMenu    = 0xA5
	vkRWin     = 0x5C
	vkApps     = 0x5D
	vkCapital  = 0x14
	vkPause    = 0x13
	vkScroll   = 0x91
	vkF13      = 0x7C
)

// holdKeys maps the names accepted in config.json to virtual-key codes.
//
// The default is right ctrl: it is on essentially every keyboard, does
// nothing on its own, and — unlike right alt, which is AltGr on non-US
// layouts — carries no second meaning. There is no fn key to use here; on
// nearly all laptops fn is handled in keyboard firmware and never reaches
// the OS at all.
var holdKeys = map[string]uint32{
	"rightctrl":  vkRControl,
	"rctrl":      vkRControl,
	"leftctrl":   vkLControl,
	"lctrl":      vkLControl,
	"rightshift": vkRShift,
	"rshift":     vkRShift,
	"leftshift":  vkLShift,
	"lshift":     vkLShift,
	"rightalt":   vkRMenu,
	"ralt":       vkRMenu,
	"leftalt":    vkLMenu,
	"lalt":       vkLMenu,
	"rightwin":   vkRWin,
	"rwin":       vkRWin,
	"menu":       vkApps,
	"apps":       vkApps,
	"capslock":   vkCapital,
	"pause":      vkPause,
	"scrolllock": vkScroll,
}

func init() {
	for i := 0; i < 12; i++ {
		holdKeys[fmt.Sprintf("f%d", 13+i)] = uint32(vkF13 + i)
	}
}

// Configure selects the push-to-talk key and whether the hook hides it from
// the rest of the system. It must be called before Start.
//
// name is a key from holdKeys; empty means the default. passThrough leaves
// the key visible to the focused application, which matters if you also use
// that key for shortcuts: with the default (swallowed) right ctrl, a
// right-handed Ctrl+C types a bare "c" instead of copying.
//
// An unrecognised name is an error, and the caller is expected to keep the
// default rather than start with no hotkey.
func Configure(name string, passThrough bool) error {
	swallow.Store(!passThrough)
	if strings.TrimSpace(name) == "" {
		holdKey.Store(vkRControl)
		return nil
	}
	vk, ok := holdKeys[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return fmt.Errorf("unknown hold key %q", name)
	}
	holdKey.Store(vk)
	return nil
}

// HoldKeyName returns the configured key's name, for the ready line.
func HoldKeyName() string {
	vk := holdKey.Load()
	switch vk {
	case vkRControl:
		return "right ctrl"
	case vkLControl:
		return "left ctrl"
	case vkRShift:
		return "right shift"
	case vkLShift:
		return "left shift"
	case vkRMenu:
		return "right alt"
	case vkLMenu:
		return "left alt"
	case vkRWin:
		return "right win"
	case vkApps:
		return "menu"
	case vkCapital:
		return "caps lock"
	case vkPause:
		return "pause"
	case vkScroll:
		return "scroll lock"
	}
	if vk >= vkF13 && vk < vkF13+12 {
		return fmt.Sprintf("F%d", 13+vk-vkF13)
	}
	return "hold key"
}

// keyWasDown dedupes: Windows repeats WM_KEYDOWN while a key is held, and
// only the transitions are dictation boundaries. Touched only on the hook
// thread.
var keyWasDown bool

func setKeyState(down bool) {
	if down == keyWasDown {
		return
	}
	keyWasDown = down
	select {
	case events <- down:
	default:
		// Unreachable in practice — the consumer only starts and stops the
		// recorder, which takes microseconds. Dropping beats blocking on the
		// one thread Windows is timing.
	}
}

// hookCallback is created once: every syscall.NewCallback consumes a slot
// from a process-wide table that is never reclaimed.
var hookCallback = syscall.NewCallback(hookProc)

// hookProc runs on the hook thread for every keystroke in the session, so it
// stays allocation-free and does nothing but classify the event and hand it
// to a channel.
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

	// Our own Ctrl+V, on its way to paste a transcript. Without this the
	// hook would see the ctrl it just synthesised and start a recording.
	if lParam.dwExtraInfo == win.InjectedTag {
		return callNext(nCode, wParam, lParam)
	}

	if lParam.vkCode != holdKey.Load() {
		return callNext(nCode, wParam, lParam)
	}

	switch wParam {
	case wmKeyDown, wmSysKeyDown:
		setKeyState(true)
	case wmKeyUp, wmSysKeyUp:
		setKeyState(false)
	default:
		return callNext(nCode, wParam, lParam)
	}

	if swallow.Load() {
		// Non-zero swallows the key: the focused application never sees it,
		// so holding it to dictate cannot disturb whatever is in front.
		return 1
	}
	return callNext(nCode, wParam, lParam)
}

func callNext(nCode, wParam uintptr, lParam *kbdllhookstruct) uintptr {
	r, _, _ := callNextHookEx.Call(0, nCode, wParam, uintptr(unsafe.Pointer(lParam)))
	return r
}

// Start registers the hold-key callbacks and begins listening. onStart fires
// the moment the key goes down; onEnd fires when it is released.
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

	go func() {
		for pressed := range events {
			if pressed {
				start()
			} else {
				end()
			}
		}
	}()

	return nil
}
