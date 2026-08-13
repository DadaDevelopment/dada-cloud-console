package ui

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// fallbackWidth is the terminal width assumed when the real one cannot be
// read. 80 is the floor every terminal emulator still honours.
const fallbackWidth = 80

// minWidth keeps a pathologically narrow terminal from collapsing the live
// line into nothing: below this the line is drawn at this width and allowed to
// wrap, which is no worse than the old behaviour.
const minWidth = 24

type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// terminalWidth reports the column count of the terminal behind f.
//
// The live line must never be longer than this. A longer line wraps, and a
// wrapped line cannot be rewritten in place: "\r\x1b[2K" moves to the start of
// the last visual row and clears only that row, so every repaint leaves the
// overflowed head of the previous frame on screen. That is what turned one
// live line into a stack of near-identical lines that read like a log.
func terminalWidth(f *os.File) int {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno == 0 && ws.cols > 0 {
		return int(ws.cols)
	}
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols > 0 {
		return cols
	}
	return fallbackWidth
}
