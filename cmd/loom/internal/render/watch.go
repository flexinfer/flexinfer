package render

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// minWatchInterval is the lowest tick frequency Watch will honor. Anything
// smaller is clamped up to this value so callers cannot accidentally pin a
// CPU.
const minWatchInterval = 500 * time.Millisecond

// ansiClearScreen homes the cursor and clears the visible screen. We emit it
// before each non-initial render so the previous frame is replaced rather
// than scrolled.
const ansiClearScreen = "\x1b[H\x1b[2J"

// Watch invokes render on a fixed cadence until ctx is canceled or render
// returns a non-nil error.
//
// Behavior:
//   - render is called once immediately, then on every tick of interval.
//   - Between ticks the screen is cleared via ANSI escapes when stdout is a
//     TTY and NO_COLOR is unset; otherwise the renders simply concatenate.
//   - Any non-nil error returned by render terminates the loop and is
//     surfaced to the caller as-is.
//   - context.Canceled from ctx.Done is treated as a clean exit and Watch
//     returns nil; other context errors (e.g. DeadlineExceeded) are returned.
//   - Interval values below 500ms are clamped to 500ms.
//
// Watch does not install any signal handlers. Callers wanting Ctrl-C
// support are expected to wire signal.NotifyContext into ctx.
func Watch(ctx context.Context, w io.Writer, interval time.Duration, render func() error) error {
	if render == nil {
		return errors.New("render: Watch requires a non-nil render function")
	}
	if interval < minWatchInterval {
		interval = minWatchInterval
	}

	useANSI := watchUsesANSI(w)

	if err := render(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case <-ticker.C:
			if useANSI {
				if _, err := io.WriteString(w, ansiClearScreen); err != nil {
					return err
				}
			}
			if err := render(); err != nil {
				return err
			}
		}
	}
}

// watchUsesANSI reports whether Watch should emit ANSI screen-clear
// sequences for w. It returns true only when w is the standard output, the
// terminal is interactive, and NO_COLOR is unset.
func watchUsesANSI(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isTerminalFile(f)
}

// isTerminalFile reports whether f is attached to a character device. It
// avoids pulling in golang.org/x/term so the helper stays self-contained.
func isTerminalFile(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
