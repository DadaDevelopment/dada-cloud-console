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
	if p.live {
		p.stop = make(chan struct{})
		p.done = make(chan struct{})
		go p.animate()
	}
	return p
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
	spin := spinnerFrames[p.frame%len(spinnerFrames)]
	line := fmt.Sprintf("\r\x1b[2K%s %s  %s %s  %s",
		p.paint("35;1", spin),
		p.label,
		bar(p.percent, p.color),
		p.paint("90", fmt.Sprintf("%3d%%", p.percent)),
		p.paint("90", elapsed(time.Since(p.start))),
	)
	fmt.Fprint(p.w, line)
	p.drawn = true
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
