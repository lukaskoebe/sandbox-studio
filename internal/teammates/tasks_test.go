package teammates

import (
	"errors"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestCheckTaskFields(t *testing.T) {
	for _, title := range []string{"Write the report", "Ünïcode ✓"} {
		if err := CheckTaskTitle(title); err != nil {
			t.Errorf("title %q: %v", title, err)
		}
	}
	for _, title := range []string{"", "   ", "two\nlines", strings.Repeat("x", MaxTaskTitleLen+1)} {
		if err := CheckTaskTitle(title); !errors.Is(err, ErrInvalid) {
			t.Errorf("title %q: %v", title, err)
		}
	}

	if err := CheckTaskDetails(strings.Repeat("a", MaxTaskDetailsSize)); err != nil {
		t.Errorf("details at the limit: %v", err)
	}
	for _, details := range []string{strings.Repeat("a", MaxTaskDetailsSize+1), "nul\x00byte", "\xff"} {
		if err := CheckTaskDetails(details); !errors.Is(err, ErrInvalid) {
			t.Errorf("details %q: %v", details[:min(len(details), 8)], err)
		}
	}

	for _, due := range []string{"", "2026-10-20"} {
		if err := CheckTaskDue(due); err != nil {
			t.Errorf("due %q: %v", due, err)
		}
	}
	for _, due := range []string{"20.10.2026", "2026-10-20T09:00", "2026-13-01", "tomorrow"} {
		if err := CheckTaskDue(due); !errors.Is(err, ErrInvalid) {
			t.Errorf("due %q: %v", due, err)
		}
	}
}

func TestCheckHeartbeat(t *testing.T) {
	for _, h := range []store.Heartbeat{
		{EveryHours: 1, FromHour: 0, ToHour: 1},
		{EveryHours: 4, FromHour: 9, ToHour: 18},
		{EveryHours: 24, FromHour: 0, ToHour: 24},
	} {
		if err := CheckHeartbeat(h); err != nil {
			t.Errorf("%+v: %v", h, err)
		}
	}
	for _, h := range []store.Heartbeat{
		{EveryHours: 0, FromHour: 9, ToHour: 18},
		{EveryHours: 25, FromHour: 9, ToHour: 18},
		{EveryHours: 4, FromHour: -1, ToHour: 18},
		{EveryHours: 4, FromHour: 24, ToHour: 24},
		{EveryHours: 4, FromHour: 9, ToHour: 0},
		{EveryHours: 4, FromHour: 9, ToHour: 25},
		{EveryHours: 4, FromHour: 18, ToHour: 9},
		{EveryHours: 4, FromHour: 9, ToHour: 9},
	} {
		if err := CheckHeartbeat(h); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", h, err)
		}
	}
}
