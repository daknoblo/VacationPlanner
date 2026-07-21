package ical

import (
	"strings"
	"testing"
	"time"
)

func TestEncodeStructure(t *testing.T) {
	now := time.Date(2026, 7, 21, 16, 24, 0, 0, time.UTC)
	cal := Calendar{
		ProdID: "-//Test//EN",
		Name:   "My Trip",
		Events: []Event{
			{
				UID:      "trip@x",
				Start:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
				End:      time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
				AllDay:   true,
				Summary:  "My Trip",
				Location: "Rome",
			},
			{
				UID:     "seg@x",
				Start:   time.Date(2026, 8, 1, 8, 30, 0, 0, time.UTC),
				End:     time.Date(2026, 8, 1, 10, 45, 0, 0, time.UTC),
				Summary: "Arrival",
			},
		},
	}
	out := string(cal.Encode(now))

	// Every line must be CRLF-terminated.
	if strings.Contains(out, "\n") && !strings.Contains(out, "\r\n") {
		t.Fatal("output must use CRLF line endings")
	}
	wants := []string{
		"BEGIN:VCALENDAR\r\n",
		"VERSION:2.0\r\n",
		"PRODID:-//Test//EN\r\n",
		"X-WR-CALNAME:My Trip\r\n",
		"BEGIN:VEVENT\r\n",
		"UID:trip@x\r\n",
		"DTSTAMP:20260721T162400Z\r\n",
		"DTSTART;VALUE=DATE:20260801\r\n",
		"DTEND;VALUE=DATE:20260806\r\n", // exclusive end = EndDate + 1 day
		"DTSTART:20260801T083000Z\r\n",
		"DTEND:20260801T104500Z\r\n",
		"END:VCALENDAR\r\n",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestEscapeText(t *testing.T) {
	cases := map[string]string{
		"a,b;c\\d":  `a\,b\;c\\d`,
		"line1\nl2": `line1\nl2`,
	}
	for in, want := range cases {
		if got := escapeText(in); got != want {
			t.Errorf("escapeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEndDefaultsToStart(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 8, 30, 0, 0, time.UTC)
	cal := Calendar{Events: []Event{{UID: "x", Start: start, End: time.Time{}, Summary: "S"}}}
	out := string(cal.Encode(now))
	if !strings.Contains(out, "DTSTART:20260801T083000Z\r\n") ||
		!strings.Contains(out, "DTEND:20260801T083000Z\r\n") {
		t.Errorf("zero end should default to start\n%s", out)
	}
}

func TestLineFolding(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 200)
	cal := Calendar{Events: []Event{{UID: "x", Start: now, Summary: long}}}
	out := string(cal.Encode(now))
	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 75 {
			t.Fatalf("line exceeds 75 octets (%d): %q", len(line), line)
		}
	}
	// Folded continuation lines begin with a single space.
	if !strings.Contains(out, "\r\n "+"") {
		t.Error("expected folded continuation lines")
	}
}
