package tasks

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// MaxScheduled bounds a teammate's tasks with a schedule that are not done. Every run calls a
// model on the user's key.
const MaxScheduled = 20

// ErrCannotRun refuses a run that can't start now, such as Run now while the teammate is busy.
// The error says why.
var ErrCannotRun = errors.New("the run can't start")

// weekdays are a schedule's days in the order the week shows them.
var weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var weekdayOf = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
	"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

// CheckSchedule validates a task's schedule: a day and time once, some weekdays and a time,
// or every 1 to 24 hours within a window of local hours that ends after it starts.
func CheckSchedule(s store.TaskSchedule) error {
	switch s.Repeat {
	case store.RepeatOnce:
		if _, err := time.Parse(time.DateOnly, s.Date); err != nil || len(s.Date) != len(time.DateOnly) {
			return fmt.Errorf("%w: a schedule that runs once needs a day as YYYY-MM-DD", teammates.ErrInvalid)
		}
		if _, _, ok := clock(s.Time); !ok {
			return fmt.Errorf("%w: the time must be HH:MM", teammates.ErrInvalid)
		}
	case store.RepeatDays:
		if len(s.Days) == 0 {
			return fmt.Errorf("%w: choose at least one day", teammates.ErrInvalid)
		}
		for _, d := range s.Days {
			if _, ok := weekdayOf[d]; !ok {
				return fmt.Errorf("%w: %q is not a day: use mon, tue, wed, thu, fri, sat or sun", teammates.ErrInvalid, d)
			}
		}
		if _, _, ok := clock(s.Time); !ok {
			return fmt.Errorf("%w: the time must be HH:MM", teammates.ErrInvalid)
		}
	case store.RepeatHours:
		switch {
		case s.EveryHours < 1 || s.EveryHours > 24:
			return fmt.Errorf("%w: runs are every 1 to 24 hours", teammates.ErrInvalid)
		case s.FromHour < 0 || s.FromHour > 23:
			return fmt.Errorf("%w: the window starts at an hour from 0 to 23", teammates.ErrInvalid)
		case s.ToHour < 1 || s.ToHour > 24:
			return fmt.Errorf("%w: the window ends at an hour from 1 to 24", teammates.ErrInvalid)
		case s.FromHour >= s.ToHour:
			return fmt.Errorf("%w: the window must end after it starts", teammates.ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: a schedule repeats once, on days or every few hours", teammates.ErrInvalid)
	}
	return nil
}

// normalizeSchedule keeps only the fields s's repeat uses, and its days once each, in the
// week's order.
func normalizeSchedule(s store.TaskSchedule) store.TaskSchedule {
	out := store.TaskSchedule{Repeat: s.Repeat, Paused: s.Paused}
	switch s.Repeat {
	case store.RepeatOnce:
		out.Date, out.Time = s.Date, s.Time
	case store.RepeatDays:
		out.Time = s.Time
		for _, d := range weekdays {
			if slices.Contains(s.Days, d) {
				out.Days = append(out.Days, d)
			}
		}
		for _, d := range s.Days {
			if _, ok := weekdayOf[d]; !ok {
				out.Days = append(out.Days, d) // kept for CheckSchedule to name
			}
		}
	case store.RepeatHours:
		out.EveryHours, out.FromHour, out.ToHour = s.EveryHours, s.FromHour, s.ToHour
	}
	return out
}

// Next returns the first run of s after after, in after's location: the scheduler passes the
// host's local time. It is zero when there is none: s is paused, or runs once at a time that
// has passed. A time that doesn't exist on a day the clocks go forward runs when it would have.
func Next(s store.TaskSchedule, after time.Time) time.Time {
	if s.Paused {
		return time.Time{}
	}
	loc := after.Location()
	y, m, d := after.Date()
	switch s.Repeat {
	case store.RepeatOnce:
		day, err := time.ParseInLocation(time.DateOnly, s.Date, loc)
		hh, mm, ok := clock(s.Time)
		if err != nil || !ok {
			return time.Time{}
		}
		if at := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc); at.After(after) {
			return at
		}
	case store.RepeatDays:
		hh, mm, ok := clock(s.Time)
		if !ok {
			return time.Time{}
		}
		for i := range 8 {
			at := time.Date(y, m, d+i, hh, mm, 0, 0, loc)
			if slices.ContainsFunc(s.Days, func(day string) bool { return weekdayOf[day] == at.Weekday() }) && at.After(after) {
				return at
			}
		}
	case store.RepeatHours:
		if s.EveryHours < 1 {
			return time.Time{}
		}
		for i := range 2 {
			for h := s.FromHour; h < s.ToHour; h += s.EveryHours {
				if at := time.Date(y, m, d+i, h, 0, 0, 0, loc); at.After(after) {
					return at
				}
			}
		}
	}
	return time.Time{}
}

// NextRun is Next as a task keeps it: nil for none.
func NextRun(s *store.TaskSchedule, after time.Time) *time.Time {
	if s == nil {
		return nil
	}
	at := Next(*s, after)
	if at.IsZero() {
		return nil
	}
	return &at
}

// clock reads a time of day, HH:MM.
func clock(s string) (hour, minute int, ok bool) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}
