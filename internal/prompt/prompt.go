// Package prompt confirms destructive actions on the controlling terminal, bypassing
// stdin/stdout so prompts still work while a shell wrapper is capturing wt's stdout.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrAborted is returned when the user declines a destructive confirmation prompt, or when
// no controlling terminal is available to ask. cmd/wt maps it to exit code 2.
var ErrAborted = errors.New("aborted at safety prompt")

// Confirm writes text to /dev/tty and reads the answer from it — never os.Stdin or
// os.Stdout. Only an exact "y", "Y", or "yes" answer proceeds; anything else, including a
// bare Enter, declines. Every destructive prompt therefore defaults to No.
func Confirm(text string) (bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, fmt.Errorf("no controlling terminal available to confirm: %w", ErrAborted)
	}
	defer tty.Close()

	if _, err := fmt.Fprint(tty, text); err != nil {
		return false, fmt.Errorf("writing prompt: %w", err)
	}

	reader := bufio.NewReader(tty)
	line, _ := reader.ReadString('\n')
	answer := strings.TrimSpace(line)

	return answer == "y" || answer == "Y" || strings.EqualFold(answer, "yes"), nil
}
