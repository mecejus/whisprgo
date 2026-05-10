package secret

/*
#include <termios.h>
#include <unistd.h>

static int whispr_set_echo(int fd, int enable) {
    struct termios t;
    if (tcgetattr(fd, &t) != 0) return -1;
    if (enable) t.c_lflag |= ECHO;
    else        t.c_lflag &= ~ECHO;
    return tcsetattr(fd, TCSANOW, &t);
}
*/
import "C"

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Read prompts for a line of input on stdin without echoing it. If echo cannot
// be disabled (e.g. stdin is not a tty), it falls back to a plain echoed read.
func Read(prompt string) (string, error) {
	fmt.Print(prompt)

	fd := C.int(os.Stdin.Fd())
	echoOff := C.whispr_set_echo(fd, 0) == 0
	defer func() {
		if echoOff {
			C.whispr_set_echo(fd, 1)
			fmt.Println()
		}
	}()

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
