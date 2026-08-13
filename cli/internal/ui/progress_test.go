package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestNonTerminalOutputStaysPlain guards the CI/pipe case: no escape codes, no
// carriage returns, one greppable line per step.
func TestNonTerminalOutputStaysPlain(t *testing.T) {
	buf := &bytes.Buffer{}
	p := New(buf)
	p.Stage("Собираем архив", 16)
	p.Info("Фреймворк: %s", "Next.js")
	p.Success("Готово: %s", "https://example.ru")

	got := buf.String()
	for _, forbidden := range []string{"\x1b", "\r"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("plain output contains %q: %q", forbidden, got)
		}
	}
	for _, want := range []string{"Собираем архив", "Next.js", "https://example.ru"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q is missing %q", got, want)
		}
	}
}

func TestStageNeverMovesBackwards(t *testing.T) {
	p := New(&bytes.Buffer{})
	p.Stage("сборка", 66)
	p.Stage("очередь", 34)
	if p.percent != 66 {
		t.Fatalf("percent = %d, want it to stay at 66", p.percent)
	}
}

func TestBarFillsProportionally(t *testing.T) {
	cases := []struct {
		percent int
		filled  int
	}{{-5, 0}, {0, 0}, {50, barWidth / 2}, {100, barWidth}, {150, barWidth}}
	for _, tc := range cases {
		got := strings.Count(bar(tc.percent, false), "█")
		if got != tc.filled {
			t.Errorf("bar(%d) has %d full cells, want %d", tc.percent, got, tc.filled)
		}
	}
}

func TestElapsedSwitchesToMinutes(t *testing.T) {
	if got := elapsed(12400 * time.Millisecond); got != "12.4s" {
		t.Errorf("elapsed(12.4s) = %q", got)
	}
	if got := elapsed(134 * time.Second); got != "2м 14с" {
		t.Errorf("elapsed(134s) = %q", got)
	}
}

// TestLiveLineIsRewrittenInPlace checks the escape sequence the TTY path
// emits: carriage return plus erase-line, so successive frames do not stack up
// as separate lines.
func TestLiveLineIsRewrittenInPlace(t *testing.T) {
	buf := &bytes.Buffer{}
	p := &Progress{w: buf, color: true, live: true, start: time.Now()}
	p.Stage("Собираем образ", 66)
	p.frame++
	p.redraw()

	got := buf.String()
	if strings.Count(got, "\r\x1b[2K") != 2 {
		t.Fatalf("expected two in-place redraws, got %q", got)
	}
	if !strings.Contains(got, "█") || !strings.Contains(got, "░") {
		t.Fatalf("bar missing from %q", got)
	}
	if !strings.Contains(got, " 66%") {
		t.Fatalf("percent missing from %q", got)
	}
}
