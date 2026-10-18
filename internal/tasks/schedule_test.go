package tasks

import (
	"errors"
	"testing"
	"time"
	_ "time/tzdata" // the tests' zones on hosts without a zone database

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

func TestNextRun(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	at := func(y int, m time.Month, d, hour, minute int) time.Time {
		return time.Date(y, m, d, hour, minute, 0, 0, berlin)
	}
	weekdays := store.TaskSchedule{Repeat: store.RepeatDays, Days: []string{"mon", "tue", "wed", "thu", "fri"}, Time: "09:00"}
	fridays := store.TaskSchedule{Repeat: store.RepeatDays, Days: []string{"fri"}, Time: "09:00"}
	every4 := store.TaskSchedule{Repeat: store.RepeatHours, EveryHours: 4, FromHour: 8, ToHour: 20}
	hourly := store.TaskSchedule{Repeat: store.RepeatHours, EveryHours: 1, FromHour: 0, ToHour: 24}
	lateNight := store.TaskSchedule{Repeat: store.RepeatHours, EveryHours: 2, FromHour: 20, ToHour: 24}
	once := store.TaskSchedule{Repeat: store.RepeatOnce, Date: "2026-10-24", Time: "14:00"}
	for _, tc := range []struct {
		name  string
		s     store.TaskSchedule
		after time.Time
		want  time.Time // zero for none
	}{
		{"once, ahead", once, at(2026, 10, 18, 12, 0), at(2026, 10, 24, 14, 0)},
		{"once, at its time", once, at(2026, 10, 24, 14, 0), time.Time{}},
		{"once, past", once, at(2026, 10, 25, 9, 0), time.Time{}},
		{"paused", store.TaskSchedule{Repeat: store.RepeatOnce, Date: "2026-10-24", Time: "14:00", Paused: true}, at(2026, 10, 18, 12, 0), time.Time{}},
		{"weekdays, later the same day", weekdays, at(2026, 10, 19, 8, 0), at(2026, 10, 19, 9, 0)},
		{"weekdays, after Friday's run", weekdays, at(2026, 10, 23, 9, 0), at(2026, 10, 26, 9, 0)},
		{"Fridays, from the end of a month", fridays, at(2026, 10, 31, 12, 0), at(2026, 11, 6, 9, 0)},
		{"Fridays, a week after a Friday's run", fridays, at(2026, 10, 30, 9, 0), at(2026, 11, 6, 9, 0)},
		{"every 4 hours, before the window", every4, at(2026, 10, 19, 6, 0), at(2026, 10, 19, 8, 0)},
		{"every 4 hours, inside the window", every4, at(2026, 10, 19, 12, 0), at(2026, 10, 19, 16, 0)},
		{"every 4 hours, the window ends before 20", every4, at(2026, 10, 19, 16, 0), at(2026, 10, 20, 8, 0)},
		{"every 4 hours, over the end of a month", every4, at(2026, 10, 31, 19, 0), at(2026, 11, 1, 8, 0)},
		{"a window that ends at midnight", lateNight, at(2026, 10, 19, 21, 0), at(2026, 10, 19, 22, 0)},
		{"a window that ends at midnight, after its last run", lateNight, at(2026, 10, 19, 22, 0), at(2026, 10, 20, 20, 0)},
		// The clocks go forward at 02:00 on 29 March: 02:00 doesn't exist, so it runs at 03:00.
		{"hourly over the spring change", hourly, at(2026, 3, 29, 1, 30), at(2026, 3, 29, 3, 0)},
		{"hourly after the spring change", hourly, at(2026, 3, 29, 3, 0), at(2026, 3, 29, 4, 0)},
		{"days at a time the spring change skips", store.TaskSchedule{Repeat: store.RepeatDays, Days: []string{"sun"}, Time: "02:30"},
			at(2026, 3, 28, 12, 0), at(2026, 3, 29, 3, 30)},
		// The clocks go back at 03:00 on 25 October: a day schedule still runs once, 25 hours later.
		{"days over the autumn change", store.TaskSchedule{Repeat: store.RepeatDays, Days: []string{"sat", "sun"}, Time: "09:00"},
			at(2026, 10, 24, 9, 0), at(2026, 10, 25, 9, 0)},
	} {
		got := Next(tc.s, tc.after)
		if !got.Equal(tc.want) {
			t.Errorf("%s: Next = %v, want %v", tc.name, got, tc.want)
		}
	}
	if d := at(2026, 10, 25, 9, 0).Sub(at(2026, 10, 24, 9, 0)); d != 25*time.Hour {
		t.Errorf("the autumn change: %v between two days at 09:00", d)
	}
}

func TestCheckSchedule(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    store.TaskSchedule
		ok   bool
	}{
		{"once", store.TaskSchedule{Repeat: "once", Date: "2026-10-24", Time: "14:00"}, true},
		{"once without a day", store.TaskSchedule{Repeat: "once", Time: "14:00"}, false},
		{"once at a bad time", store.TaskSchedule{Repeat: "once", Date: "2026-10-24", Time: "2pm"}, false},
		{"days", store.TaskSchedule{Repeat: "days", Days: []string{"mon"}, Time: "09:00"}, true},
		{"days without days", store.TaskSchedule{Repeat: "days", Time: "09:00"}, false},
		{"days with a day that isn't one", store.TaskSchedule{Repeat: "days", Days: []string{"monday"}, Time: "09:00"}, false},
		{"days at 24:00", store.TaskSchedule{Repeat: "days", Days: []string{"mon"}, Time: "24:00"}, false},
		{"hours", store.TaskSchedule{Repeat: "hours", EveryHours: 4, FromHour: 8, ToHour: 20}, true},
		{"hours to midnight", store.TaskSchedule{Repeat: "hours", EveryHours: 1, FromHour: 0, ToHour: 24}, true},
		{"hours more often than hourly", store.TaskSchedule{Repeat: "hours", EveryHours: 0, FromHour: 8, ToHour: 20}, false},
		{"hours with an empty window", store.TaskSchedule{Repeat: "hours", EveryHours: 1, FromHour: 20, ToHour: 8}, false},
		{"a cron line", store.TaskSchedule{Repeat: "cron"}, false},
	} {
		err := CheckSchedule(tc.s)
		if tc.ok != (err == nil) || err != nil && !errors.Is(err, teammates.ErrInvalid) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	got := normalizeSchedule(store.TaskSchedule{Repeat: "days", Days: []string{"fri", "mon", "fri"}, Time: "09:00", EveryHours: 3, Date: "2026-10-24"})
	if len(got.Days) != 2 || got.Days[0] != "mon" || got.Days[1] != "fri" || got.EveryHours != 0 || got.Date != "" {
		t.Errorf("normalized: %+v", got)
	}
}
