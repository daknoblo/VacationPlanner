package applog

import (
	"log/slog"
	"os"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "INFO": slog.LevelInfo,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError,
	}
	for name, want := range cases {
		got, ok := ParseLevel(name)
		if !ok || got != want {
			t.Fatalf("ParseLevel(%q) = %v, %v; want %v", name, got, ok, want)
		}
	}
	if _, ok := ParseLevel("bogus"); ok {
		t.Fatal("bogus level should be invalid")
	}
}

func TestBufferCaptureAndLevel(t *testing.T) {
	// Bind the logger to /dev/null so the test produces no stdout noise.
	old := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stdout = devnull
	defer func() { os.Stdout = old; _ = devnull.Close() }()

	logger, ctrl := New("development")
	logger.Info("hello", "k", "v")
	logger.Error("boom", "err", "bad")

	recent := ctrl.Recent(10)
	if len(recent) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(recent))
	}
	if recent[0].Message != "boom" || recent[0].Level != "error" {
		t.Fatalf("newest entry mismatch: %+v", recent[0])
	}
	if recent[1].Message != "hello" || recent[1].Attrs != "k=v" {
		t.Fatalf("older entry mismatch: %+v", recent[1])
	}

	// Raising the level suppresses lower-severity records.
	ctrl.SetLevel(slog.LevelWarn)
	if ctrl.LevelName() != "warn" {
		t.Fatalf("LevelName = %q, want warn", ctrl.LevelName())
	}
	logger.Info("ignored")
	if got := len(ctrl.Recent(10)); got != 2 {
		t.Fatalf("info at warn level must not be captured; got %d entries", got)
	}
}
