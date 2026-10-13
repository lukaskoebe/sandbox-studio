package teammates

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits of task fields. The details are bounded in bytes, as a soul is.
const (
	MaxTaskTitleLen    = 200
	MaxTaskDetailsSize = 8 << 10
)

// CheckTaskTitle validates a task's title: one line of text.
func CheckTaskTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("%w: the task needs a title", ErrInvalid)
	}
	if utf8.RuneCountInString(title) > MaxTaskTitleLen {
		return fmt.Errorf("%w: the task title is longer than %d characters", ErrInvalid, MaxTaskTitleLen)
	}
	return checkLine("task title", title)
}

// CheckTaskDetails validates a task's details: markdown of bounded size without NUL bytes.
func CheckTaskDetails(details string) error {
	switch {
	case len(details) > MaxTaskDetailsSize:
		return fmt.Errorf("%w: the details are larger than %d KiB", ErrInvalid, MaxTaskDetailsSize>>10)
	case !utf8.ValidString(details):
		return fmt.Errorf("%w: the details are not valid UTF-8", ErrInvalid)
	case strings.IndexByte(details, 0) >= 0:
		return fmt.Errorf("%w: the details contain a NUL byte", ErrInvalid)
	}
	return nil
}

// CheckTaskDue validates a task's due date: a day as YYYY-MM-DD, or empty for none.
func CheckTaskDue(due string) error {
	if due == "" {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, due); err != nil {
		return fmt.Errorf("%w: the due date must be a day as YYYY-MM-DD", ErrInvalid)
	}
	return nil
}
