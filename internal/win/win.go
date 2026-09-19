//go:build windows

// Package win holds the Win32 plumbing shared by whisprgo's Windows-only
// packages: safe system-DLL loading, UTF-16 conversion, and the tag that
// marks the keystrokes whisprgo itself injects.
package win

import (
	"strings"
	"sync"
	"syscall"
)

// InjectedTag marks keyboard events whisprgo synthesises — the Ctrl+V that
// pastes a transcript. SendInput carries it in dwExtraInfo and the low-level
// keyboard hook checks for it, so the hook can never mistake our own paste
// for the user working the hold key. Any non-zero value distinguishes us
// from the 0 that real hardware carries; this one is "wGPE" as bytes.
const InjectedTag = 0x77475045

func init() {
	// Resolve system DLLs out of System32 only. The default search order
	// looks in the directory the .exe sits in first, so without this anyone
	// who can write next to whisprgo.exe could have their own winmm.dll
	// loaded into a process that reads the microphone. kernel32 is a
	// KnownDLL, which is already resolved from System32, so loading this one
	// by name is safe.
	//
	// Every other DLL here is opened through a LazyDLL, which does not touch
	// the filesystem until the first Call — long after this init has run.
	k := syscall.NewLazyDLL("kernel32.dll")
	if p := k.NewProc("SetDefaultDllDirectories"); p.Find() == nil {
		const loadLibrarySearchSystem32 = 0x800
		p.Call(loadLibrarySearchSystem32)
	}
}

var (
	dllsMu sync.Mutex
	dlls   = map[string]*syscall.LazyDLL{}
)

// Proc resolves a procedure in a system DLL. Resolution is lazy, so a missing
// entry point surfaces at the first call rather than at startup.
func Proc(dll, name string) *syscall.LazyProc {
	dllsMu.Lock()
	defer dllsMu.Unlock()
	d, ok := dlls[dll]
	if !ok {
		d = syscall.NewLazyDLL(dll)
		dlls[dll] = d
	}
	return d.NewProc(name)
}

// UTF16Ptr converts s to a NUL-terminated UTF-16 string for the W-suffixed
// Win32 entry points. Interior NULs are stripped rather than rejected: this
// is called with transcripts and error text, where failing the whole call
// over a stray byte would be worse than dropping it.
func UTF16Ptr(s string) *uint16 {
	if strings.ContainsRune(s, 0) {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		// Unreachable: the only error UTF16PtrFromString returns is for an
		// interior NUL, which is gone by now.
		empty := uint16(0)
		return &empty
	}
	return p
}

// UTF16ToString decodes a UTF-16 buffer copied out of Win32 memory, stopping
// at the first NUL. A buffer with no terminator is taken whole rather than
// read past, because Win32 allocation sizes are rounded up and the slack is
// not ours to interpret.
func UTF16ToString(buf []uint16) string {
	for i, c := range buf {
		if c == 0 {
			return syscall.UTF16ToString(buf[:i])
		}
	}
	return syscall.UTF16ToString(buf)
}
