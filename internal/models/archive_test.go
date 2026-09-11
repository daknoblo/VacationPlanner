package models

import (
	"testing"
	"time"
)

func TestVacationEndedUsesLocalCalendarDay(t *testing.T) {
	v := Vacation{EndDate: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{
		{time.Date(2026, 10, 24, 23, 59, 59, 0, time.FixedZone("east", 2*3600)), false},
		{time.Date(2026, 10, 25, 0, 0, 0, 0, time.FixedZone("east", 2*3600)), true},
		{time.Date(2026, 10, 25, 5, 0, 0, 0, time.UTC).In(time.FixedZone("west", -10*3600)), false},
		{time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC).In(time.FixedZone("west", -10*3600)), true},
	} {
		if got := v.Ended(tc.now); got != tc.want {
			t.Errorf("Ended(%s) = %v, want %v", tc.now, got, tc.want)
		}
	}
}
