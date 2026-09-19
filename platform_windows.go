package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"whisprgo/config"
	"whisprgo/internal/win"
	"whisprgo/keyboard"
)

// macOS can point at /System/Library/Sounds because the chimes ship with the
// OS. Windows' own sounds are named differently across versions, so whisprgo
// carries its own pair and writes them out beside the config on first run.
// They are ordinary WAV files: replace either one and whisprgo uses yours.
//
//go:embed sounds/start.wav
var startChimeWAV []byte

//go:embed sounds/end.wav
var endChimeWAV []byte

var (
	startChime string
	endChime   string
)

// holdKeyName is a var here, unlike the constant on macOS, because the key is
// configurable: there is no single Windows key as reliably meaningless as fn,
// so the default is a combination and has to be overridable. init resets this
// once config.json has been read.
var holdKeyName = keyboard.HoldKeyName()

const (
	swHide = 0

	// Keep the log from growing without bound on a machine that is never
	// restarted. A megabyte is thousands of dictations.
	maxLogBytes = 1 << 20
)

var (
	getConsoleWindow = win.Proc("kernel32.dll", "GetConsoleWindow")
	showWindow       = win.Proc("user32.dll", "ShowWindow")
)

func init() {
	// Windows has no launchd, so the installer starts whisprgo from the
	// per-user Run key with --background. Run from a terminal instead and it
	// behaves like the Mac binary does when you run it by hand: live output,
	// Ctrl-C to quit.
	if isBackground() {
		detachConsole()
	}

	startChime, endChime = installChimes()

	// A second config read, separate from the one in main: this has to happen
	// before keyboard.Start, and main's copy exists to prompt for an API key.
	// The file is a few hundred bytes.
	if cfg, err := config.Load(); err == nil {
		if err := keyboard.Configure(cfg.HoldKey, cfg.PassThroughHoldKey); err != nil {
			fmt.Fprintf(os.Stderr, "%v — falling back to right ctrl.\n", err)
			_ = keyboard.Configure("", cfg.PassThroughHoldKey)
		}
	}
	holdKeyName = keyboard.HoldKeyName()
}

func isBackground() bool {
	for _, arg := range os.Args[1:] {
		if arg == "--background" || arg == "-background" {
			return true
		}
	}
	return false
}

// detachConsole sends the status lines to a log file and hides the console
// window the Run key gave us. Redirection happens first, so nothing is
// written to a window that is about to disappear.
func detachConsole() {
	if f, err := openLog(); err == nil {
		os.Stdout = f
		os.Stderr = f
	}
	if hwnd, _, _ := getConsoleWindow.Call(); hwnd != 0 {
		showWindow.Call(hwnd, swHide)
	}
}

func openLog() (*os.File, error) {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "whisprgo.log")
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogBytes {
		os.Remove(path)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// installChimes writes the embedded WAVs next to the config unless they are
// already there, and returns the paths to load. An existing file is never
// overwritten, so a chime the user swapped out survives an upgrade.
func installChimes() (start, end string) {
	dir := filepath.Join(config.Dir(), "sounds")
	start = filepath.Join(dir, "start.wav")
	end = filepath.Join(dir, "end.wav")

	if writeChimes(dir, start, end) == nil {
		return start, end
	}

	// A locked-down profile can make the config folder unwritable. Temp is
	// the fallback; losing the chimes on an upgrade is better than not
	// starting.
	dir = filepath.Join(os.TempDir(), "whisprgo-sounds")
	fallbackStart := filepath.Join(dir, "start.wav")
	fallbackEnd := filepath.Join(dir, "end.wav")
	if writeChimes(dir, fallbackStart, fallbackEnd) == nil {
		return fallbackStart, fallbackEnd
	}

	// Both failed. Return the first pair anyway so the startup error names a
	// path the user can actually go and look at.
	return start, end
}

func writeChimes(dir, start, end string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writeIfMissing(start, startChimeWAV); err != nil {
		return err
	}
	return writeIfMissing(end, endChimeWAV)
}

func writeIfMissing(path string, data []byte) error {
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return nil
	}
	return os.WriteFile(path, data, 0o600)
}
