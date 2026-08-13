package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestLiveLineNeverOutgrowsTheTerminal is the regression lock for the deploy a
// user screenshotted on 2026-08-13: the live line was longer than the window,
// so it wrapped, and "\r\x1b[2K" then cleared only its last visual row. Every
// repaint left the overflowed head behind and one animated line looked like a
// stack of duplicated log lines.
func TestLiveLineNeverOutgrowsTheTerminal(t *testing.T) {
	long := "Переключаем на keksmd/family-tree-with-a-very-long-name"
	for _, width := range []int{24, 40, 60, 80, 120} {
		for _, color := range []bool{false, true} {
			p := &Progress{w: &bytes.Buffer{}, color: color, live: true,
				start: time.Now(), label: long, percent: 12}
			got := visibleWidth(p.renderLine(width))
			if got >= width {
				t.Fatalf("width %d color %v: line takes %d columns, must stay under %d",
					width, color, got, width)
			}
		}
	}
}

// TestShortLabelIsLeftAlone keeps the truncation from firing when it is not
// needed: a label that fits must be printed whole, ellipsis-free.
func TestShortLabelIsLeftAlone(t *testing.T) {
	p := &Progress{w: &bytes.Buffer{}, live: true, start: time.Now(),
		label: "Сборка в очереди", percent: 30}
	line := p.renderLine(120)
	if !strings.Contains(line, "Сборка в очереди") {
		t.Fatalf("label was mangled although it fits: %q", line)
	}
	if strings.Contains(line, "…") {
		t.Fatalf("label that fits must not be truncated: %q", line)
	}
}

// TestTruncateMarksTheCut keeps a shortened label recognizable as shortened.
func TestTruncateMarksTheCut(t *testing.T) {
	if got := truncate("Собираем образ", 6); got != "Собир…" {
		t.Fatalf("truncate = %q, want %q", got, "Собир…")
	}
	if got := truncate("abc", 10); got != "abc" {
		t.Fatalf("truncate must not touch a fitting string, got %q", got)
	}
	if got := truncate("abc", 0); got != "" {
		t.Fatalf("zero budget must render nothing, got %q", got)
	}
}
