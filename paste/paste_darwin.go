package paste

import (
	"fmt"
	"os/exec"
	"strings"
)

// Paste writes text to the clipboard and simulates Cmd+V into the active window.
// Requires Accessibility access and Automation permission for the calling terminal.
func Paste(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pbcopy: %w", err)
	}

	script := `tell application "System Events" to keystroke "v" using command down`
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		return fmt.Errorf("osascript: %w", err)
	}
	return nil
}
