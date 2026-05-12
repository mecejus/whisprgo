package dialog

import (
	"os"
	"os/exec"
	"strings"
)

const title = "whisprgo"

// Error displays a blocking error dialog with the given message.
func Error(message string) {
	show(message)
}

// Info displays a blocking informational dialog with the given message.
func Info(message string) {
	show(message)
}

// Prompt shows a text-entry dialog and returns the user's input. The second
// return is false if the user cancelled or osascript failed.
func Prompt(message string) (string, bool) {
	const script = `set r to display dialog (system attribute "WHISPRGO_MSG") with title (system attribute "WHISPRGO_TITLE") default answer "" with hidden answer
return text returned of r`
	cmd := exec.Command("osascript", "-e", script)
	cmd.Env = append(os.Environ(),
		"WHISPRGO_MSG="+message,
		"WHISPRGO_TITLE="+title,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

func show(message string) {
	const script = `display dialog (system attribute "WHISPRGO_MSG") with title (system attribute "WHISPRGO_TITLE") buttons {"OK"} default button "OK"`
	cmd := exec.Command("osascript", "-e", script)
	cmd.Env = append(os.Environ(),
		"WHISPRGO_MSG="+message,
		"WHISPRGO_TITLE="+title,
	)
	_ = cmd.Run()
}
