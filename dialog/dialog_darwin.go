package dialog

import (
	"os"
	"os/exec"
	"strings"
)

const appTitle = "whisprgo"

// Error displays a blocking error dialog with the given message.
func Error(message string) { show(message) }

// Info displays a blocking informational dialog with the given message.
func Info(message string) { show(message) }

// Prompt shows a text-entry dialog and returns the user's input. The second
// return is false if the user cancelled or osascript failed.
func Prompt(message string) (string, bool) {
	path, ok := writeTempMsg(message)
	if !ok {
		return "", false
	}
	defer os.Remove(path)

	script := `set msg to read POSIX file "` + path + `" as «class utf8»
set r to display dialog msg with title "` + appTitle + `" default answer "" with hidden answer
return text returned of r`

	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

func show(message string) {
	path, ok := writeTempMsg(message)
	if !ok {
		return
	}
	defer os.Remove(path)

	script := `set msg to read POSIX file "` + path + `" as «class utf8»
display dialog msg with title "` + appTitle + `" buttons {"OK"} default button "OK"`

	_ = exec.Command("osascript", "-e", script).Run()
}

func writeTempMsg(message string) (string, bool) {
	f, err := os.CreateTemp("", "whisprgo-msg-*")
	if err != nil {
		return "", false
	}
	defer f.Close()
	if _, err := f.WriteString(message); err != nil {
		os.Remove(f.Name())
		return "", false
	}
	return f.Name(), true
}
