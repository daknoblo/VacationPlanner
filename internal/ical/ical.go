// Package ical builds RFC 5545 (iCalendar) documents. It is dependency-free and
// intentionally minimal: enough to publish a vacation's travel segments as a
// downloadable calendar feed.
package ical

import (
	"strings"
	"time"
)

// Event is a single calendar entry. Timed events use Start/End as instants
// (encoded in UTC); all-day events use only the date part of Start, with End
// treated as the exclusive end date per RFC 5545.
type Event struct {
	UID         string
	Start       time.Time
	End         time.Time
	AllDay      bool
	Summary     string
	Location    string
	Description string
}

// Calendar is a set of events with a product identifier and optional name.
type Calendar struct {
	ProdID string
	Name   string
	Events []Event
}

// Encode renders the calendar as a CRLF-delimited iCalendar document. The now
// argument is used for each event's DTSTAMP so callers can produce
// deterministic output.
func (c Calendar) Encode(now time.Time) []byte {
	prodID := c.ProdID
	if prodID == "" {
		prodID = "-//VacationPlanner//EN"
	}
	var b strings.Builder
	writeLine(&b, "BEGIN:VCALENDAR")
	writeLine(&b, "VERSION:2.0")
	writeLine(&b, "PRODID:"+escapeText(prodID))
	writeLine(&b, "CALSCALE:GREGORIAN")
	writeLine(&b, "METHOD:PUBLISH")
	if c.Name != "" {
		writeLine(&b, "X-WR-CALNAME:"+escapeText(c.Name))
	}
	stamp := "DTSTAMP:" + formatUTC(now)
	for _, ev := range c.Events {
		writeLine(&b, "BEGIN:VEVENT")
		writeLine(&b, "UID:"+escapeText(ev.UID))
		writeLine(&b, stamp)
		if ev.AllDay {
			writeLine(&b, "DTSTART;VALUE=DATE:"+formatDate(ev.Start))
			end := ev.End
			if !end.After(ev.Start) {
				end = ev.Start
			}
			// DTEND is exclusive for all-day events, so advance one day.
			writeLine(&b, "DTEND;VALUE=DATE:"+formatDate(end.AddDate(0, 0, 1)))
		} else {
			writeLine(&b, "DTSTART:"+formatUTC(ev.Start))
			end := ev.End
			if !end.After(ev.Start) {
				end = ev.Start
			}
			writeLine(&b, "DTEND:"+formatUTC(end))
		}
		if ev.Summary != "" {
			writeLine(&b, "SUMMARY:"+escapeText(ev.Summary))
		}
		if ev.Location != "" {
			writeLine(&b, "LOCATION:"+escapeText(ev.Location))
		}
		if ev.Description != "" {
			writeLine(&b, "DESCRIPTION:"+escapeText(ev.Description))
		}
		writeLine(&b, "END:VEVENT")
	}
	writeLine(&b, "END:VCALENDAR")
	return []byte(b.String())
}

// formatUTC renders an instant as a UTC iCalendar date-time (e.g. 20260721T162400Z).
func formatUTC(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// formatDate renders the date part of an instant (in UTC) as YYYYMMDD.
func formatDate(t time.Time) string {
	return t.UTC().Format("20060102")
}

// escapeText escapes the characters that are special inside an iCalendar TEXT
// value, per RFC 5545 §3.3.11.
func escapeText(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		";", "\\;",
		",", "\\,",
		"\r\n", "\\n",
		"\n", "\\n",
		"\r", "\\n",
	)
	return r.Replace(s)
}

// writeLine appends a content line, folding it to at most 75 octets per line as
// required by RFC 5545 §3.1, and terminating with CRLF.
func writeLine(b *strings.Builder, line string) {
	const limit = 75
	if len(line) <= limit {
		b.WriteString(line)
		b.WriteString("\r\n")
		return
	}
	// Fold on octet boundaries without splitting a multi-byte UTF-8 sequence.
	i := 0
	first := true
	for i < len(line) {
		max := limit
		if !first {
			max = limit - 1 // account for the leading fold space
		}
		end := i + max
		if end > len(line) {
			end = len(line)
		}
		// Back off so we never cut in the middle of a UTF-8 rune.
		for end > i && end < len(line) && !utf8Boundary(line[end]) {
			end--
		}
		if !first {
			b.WriteString(" ")
		}
		b.WriteString(line[i:end])
		b.WriteString("\r\n")
		i = end
		first = false
	}
}

// utf8Boundary reports whether b is the first octet of a UTF-8 sequence (i.e.
// not a continuation byte).
func utf8Boundary(b byte) bool {
	return b&0xC0 != 0x80
}
