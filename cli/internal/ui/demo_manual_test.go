package ui

import (
	"os"
	"testing"
	"time"
)

// TestManualDemo draws the live line on the real terminal so a human can see
// whether it rewrites in place. Skipped unless UI_DEMO is set.
func TestManualDemo(t *testing.T) {
	if os.Getenv("UI_DEMO") == "" {
		t.Skip("set UI_DEMO=1 to watch the live line")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no tty")
	}
	defer tty.Close()
	p := New(tty)
	for _, s := range []struct {
		label string
		pct   int
	}{
		{"Переключаем на keksmd/family-tree", 12},
		{"Собираем архив: 49 файлов, 3.6МБ", 16},
		{"Собираем образ", 66},
	} {
		p.Stage(s.label, s.pct)
		time.Sleep(700 * time.Millisecond)
	}
	p.Success("готово")
}
