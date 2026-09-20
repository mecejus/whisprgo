//go:build windows

package dialog

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"whisprgo/internal/win"
)

const appTitle = "whisprgo"

const (
	mbOK            = 0x00000000
	mbIconError     = 0x00000010
	mbSetForeground = 0x00010000
	mbTopMost       = 0x00040000
)

const (
	// createNoWindow keeps the PowerShell host from flashing a console
	// window.
	//
	// Deliberately not SysProcAttr.HideWindow, which is how this was first
	// written. That sets STARTF_USESHOWWINDOW with SW_HIDE, and SW_HIDE then
	// becomes the default show state for the *first window the process
	// creates* — which is the dialog itself. The box was drawn, invisibly, so
	// nobody could dismiss it, so ShowDialog never returned and whisprgo hung
	// at startup with no output at all. CREATE_NO_WINDOW suppresses the
	// console without touching the show state of anything drawn later.
	createNoWindow = 0x08000000

	// promptTimeout bounds the wait for an answer. Generous, because someone
	// may go off and create a Groq account first, but finite: a prompt that
	// cannot be answered has to fail with a message rather than hang the
	// process forever.
	promptTimeout = 5 * time.Minute
)

var messageBoxW = win.Proc("user32.dll", "MessageBoxW")

// Error displays a blocking error dialog with the given message.
//
// MessageBoxW needs no helper process, so unlike the macOS side there is no
// osascript to start. It still blocks until the user clicks OK, so the rule
// it shares with the Mac holds: never call this on the dictation path.
func Error(message string) {
	text := win.UTF16Ptr(message)
	title := win.UTF16Ptr(appTitle)
	messageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		mbOK|mbIconError|mbSetForeground|mbTopMost,
	)
	// LazyProc.Call forwards its arguments through a slice, so the compiler's
	// "keep the object alive for the duration of the call" rule for syscalls
	// does not reach these. Say it explicitly.
	runtime.KeepAlive(text)
	runtime.KeepAlive(title)
}

// Prompt shows a masked text-entry dialog and returns the user's input. The
// second return is false if the user cancelled or the dialog could not be
// shown.
//
// Windows has no MessageBox that takes input, so this borrows the WinForms
// assembly through Windows PowerShell, which is present on every Windows 10
// and 11 install. It runs once, on first launch, to collect the API key —
// never on the dictation path — so the cost of starting a PowerShell host
// does not matter here.
func Prompt(message string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
	defer cancel()

	// -EncodedCommand takes UTF-16LE base64. It sidesteps every layer of
	// quoting between here and PowerShell's parser, and unlike a script file
	// it is not subject to the execution policy.
	//
	// No -NonInteractive: this is a dialog, which is the one thing that
	// switch exists to suppress.
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile",
		"-Sta", // WinForms requires a single-threaded apartment.
		"-EncodedCommand", encodeCommand(promptScript(message)),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}

	out, err := cmd.Output()
	if err != nil {
		// Every one of these used to be a silent `return "", false`, which is
		// why the first report of this was "nothing happens". Whatever goes
		// wrong here, say so: on the installed path this reaches the log.
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "No answer to the API key dialog within %s.\n", promptTimeout)
		} else {
			fmt.Fprintf(os.Stderr, "Could not show the API key dialog: %v\n", err)
		}
		return "", false
	}
	if len(out) == 0 {
		// The script prints nothing when the dialog is cancelled.
		return "", false
	}

	// The answer comes back base64'd too, so neither the console code page
	// nor a stray newline can corrupt an API key on its way out.
	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not read the API key back from the dialog: %v\n", err)
		return "", false
	}
	return string(decoded), true
}

// promptScript builds the WinForms dialog. The message is passed as base64
// and decoded inside the script rather than interpolated, so no quote or
// newline in it can change what PowerShell executes.
func promptScript(message string) string {
	msg := base64.StdEncoding.EncodeToString([]byte(message))
	return `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

$form = New-Object System.Windows.Forms.Form
$form.Text = '` + appTitle + `'
$form.ClientSize = New-Object System.Drawing.Size(460,170)
$form.StartPosition = 'CenterScreen'
$form.FormBorderStyle = 'FixedDialog'
$form.MaximizeBox = $false
$form.MinimizeBox = $false
$form.TopMost = $true

$label = New-Object System.Windows.Forms.Label
$label.Text = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + msg + `'))
$label.SetBounds(14,14,432,60)
$form.Controls.Add($label)

$box = New-Object System.Windows.Forms.TextBox
$box.UseSystemPasswordChar = $true
$box.SetBounds(14,84,432,24)
$form.Controls.Add($box)

$ok = New-Object System.Windows.Forms.Button
$ok.Text = 'OK'
$ok.DialogResult = [System.Windows.Forms.DialogResult]::OK
$ok.SetBounds(268,124,84,30)
$form.Controls.Add($ok)

$cancel = New-Object System.Windows.Forms.Button
$cancel.Text = 'Cancel'
$cancel.DialogResult = [System.Windows.Forms.DialogResult]::Cancel
$cancel.SetBounds(362,124,84,30)
$form.Controls.Add($cancel)

$form.AcceptButton = $ok
$form.CancelButton = $cancel
$form.Add_Shown({ $form.Activate(); $box.Focus() })

if ($form.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($box.Text)))
}
`
}

// encodeCommand renders s as the UTF-16LE base64 blob -EncodedCommand wants.
func encodeCommand(s string) string {
	u16 := utf16.Encode([]rune(s))
	b := make([]byte, len(u16)*2)
	for i, c := range u16 {
		b[i*2] = byte(c)
		b[i*2+1] = byte(c >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
