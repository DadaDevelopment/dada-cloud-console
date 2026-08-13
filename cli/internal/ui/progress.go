// Package ui draws the deploy's progress in a terminal: a branded line per
// finished step, plus one live line with a spinner, a bar and elapsed time
// that is rewritten in place while a step runs.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	brand      = "DADA"
	barWidth   = 18
	frameEvery = 90 * time.Millisecond
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Progress renders deploy steps. Every method is safe to call from the
// goroutine driving the deploy; the animation runs on its own ticker.
type Progress struct {
	w     io.Writer
	tty   *os.File
	color bool
	live  bool
	start time.Time

	mu      sync.Mutex
	label   string
	percent int
	frame   int
	drawn   bool
	paused  bool

	stop chan struct{}
	done chan struct{}
}

// New builds a Progress for w. Animation and color are enabled only when w is
// a terminal, so piped or CI output stays a plain, greppable log.
func New(w io.Writer) *Progress {
	tty := isTerminal(w)
	p := &Progress{w: w, color: tty, live: tty, start: time.Now()}
	if f, ok := w.(*os.File); ok && tty {
		p.tty = f
	}
	if p.live {
		p.stop = make(chan struct{})
		p.done = make(chan struct{})
		fmt.Fprint(p.w, hideCursor)
		go p.animate()
	}
	return p
}

// hideCursor/showCursor keep the block cursor from riding at the end of the
// live line, where it blinks over the elapsed time on every repaint.
const (
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
)

// width returns how many columns the live line may occupy, re-read every frame
// so a terminal resized mid-deploy keeps redrawing in place.
func (p *Progress) width() int {
	if p.tty == nil {
		return fallbackWidth
	}
	w := terminalWidth(p.tty)
	if w < minWidth {
		return minWidth
	}
	return w
}

// truncate shortens s to at most n runes, marking the cut with an ellipsis.
// It is only ever applied to plain text (the stage label), never to a string
// carrying escape codes, so counting runes is counting columns.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" || os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (p *Progress) paint(code, s string) string {
	if !p.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Info prints a finished step above the live line and leaves it on screen.
func (p *Progress) Info(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLine()
	fmt.Fprintf(p.w, "%s %s %s\n", p.paint("35;1", brand), p.paint("90", "→"), msg)
	p.redraw()
}

// Stage sets what the live line says and how full its bar is. percent is the
// share of the whole deploy this step has reached; it never moves backwards.
func (p *Progress) Stage(label string, percent int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if label == p.label && percent == p.percent {
		return
	}
	p.label = label
	if percent > p.percent {
		p.percent = percent
	}
	if !p.live {
		fmt.Fprintf(p.w, "%s → %s\n", brand, label)
		return
	}
	p.redraw()
}

// Success clears the live line and prints the final result with the total
// wall-clock time of the deploy.
func (p *Progress) Success(format string, args ...any) {
	p.Stop()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(p.w, "%s %s %s\n", p.paint("32;1", "✓"), msg, p.paint("90", "("+elapsed(time.Since(p.start))+")"))
}

// Pause freezes the animation and clears the live line so a prompt can own
// the terminal. Resume brings the same line back.
func (p *Progress) Pause() {
	if !p.live {
		return
	}
	p.paused = true
	p.halt()
}

// Resume restarts the animation after a prompt has finished.
func (p *Progress) Resume() {
	if !p.paused {
		return
	}
	p.paused = false
	p.live = true
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	fmt.Fprint(p.w, hideCursor)
	go p.animate()
}

// Stop ends the animation and clears the live line, leaving the cursor at the
// start of an empty line so an error can be printed under it.
func (p *Progress) Stop() {
	p.paused = false
	p.halt()
}

// halt joins the animation goroutine before touching live state, so the
// writer of p.live is the only goroutine left running.
func (p *Progress) halt() {
	if p.live {
		close(p.stop)
		<-p.done
		p.live = false
		fmt.Fprint(p.w, showCursor)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearLine()
}

func (p *Progress) animate() {
	ticker := time.NewTicker(frameEvery)
	defer ticker.Stop()
	defer close(p.done)
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			p.frame++
			p.redraw()
			p.mu.Unlock()
		}
	}
}

func (p *Progress) clearLine() {
	if !p.drawn {
		return
	}
	fmt.Fprint(p.w, "\r\x1b[2K")
	p.drawn = false
}

func (p *Progress) redraw() {
	if !p.live || p.label == "" {
		return
	}
	fmt.Fprint(p.w, p.renderLine(p.width()))
	p.drawn = true
}

// renderLine builds the live line for a terminal of the given width, escape
// codes included and the leading carriage-return-and-clear included, so a
// caller only has to write it.
func (p *Progress) renderLine(width int) string {
	spin := spinnerFrames[p.frame%len(spinnerFrames)]
	pct := fmt.Sprintf("%3d%%", p.percent)
	el := elapsed(time.Since(p.start))

	full := "  " + bar(p.percent, p.color) + " " + p.paint("90", pct) + "  " + p.paint("90", el)
	noBar := "  " + p.paint("90", pct) + "  " + p.paint("90", el)
	bare := "  " + p.paint("90", pct)

	available := width - 1 - spinnerCost
	tail := bare
	switch {
	case available-visibleWidth(full) >= minLabel:
		tail = full
	case available-visibleWidth(noBar) >= minLabel:
		tail = noBar
	}

	label := truncate(p.label, available-visibleWidth(tail))
	return "\r\x1b[2K" + p.paint("35;1", spin) + " " + label + tail
}

// spinnerCost is the columns the spinner and the space after it always take.
const spinnerCost = 2

// minLabel is how much of the stage label must survive for a decoration to be
// worth its columns. Below it the line drops the bar, then the elapsed time:
// the words say what is happening, the bar only says it prettily.
const minLabel = 8

// visibleWidth counts the columns a rendered line occupies, ignoring the ANSI
// escape sequences inside it. It exists for the tests: staying under the
// terminal width is the whole point of labelBudget.
func visibleWidth(s string) int {
	n := 0
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
		case r == '\x1b':
			inEscape = true
		case r == '\r':
		default:
			n++
		}
	}
	return n
}

func bar(percent int, color bool) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := percent * barWidth / 100
	full := strings.Repeat("█", filled)
	empty := strings.Repeat("░", barWidth-filled)
	if !color {
		return full + empty
	}
	return "\x1b[35m" + full + "\x1b[90m" + empty + "\x1b[0m"
}

func elapsed(d time.Duration) string {
	secs := int(d.Round(time.Second).Seconds())
	if secs < 60 {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dм %02dс", secs/60, secs%60)
}
