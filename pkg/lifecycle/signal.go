// Package lifecycle provides utilities for managing application lifecycle,
// including signal handling with proper cleanup.
package lifecycle

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// RunWithSignals runs the provided function with a context that is cancelled
// when SIGINT or SIGTERM is received. It properly cleans up the signal handler
// when the function returns or the context is cancelled.
//
// Usage:
//
//	func main() {
//	    if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
//	        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
//	        os.Exit(1)
//	    }
//	}
//
//	func run(ctx context.Context) error {
//	    server := mcp.NewServer("my-server", version)
//	    return server.Run(ctx)
//	}
func RunWithSignals(ctx context.Context, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			// Context cancelled externally, goroutine exits cleanly
			return
		}
	}()

	return fn(ctx)
}

// SetupSignalHandler creates a context that is cancelled when SIGINT or SIGTERM
// is received. It returns the context, a cancel function, and a cleanup function
// that should be called when the application exits.
//
// This is useful when you need more control over the signal handling than
// RunWithSignals provides.
//
// Usage:
//
//	ctx, cancel, cleanup := lifecycle.SetupSignalHandler(context.Background())
//	defer cleanup()
//	defer cancel()
//
//	// ... use ctx ...
func SetupSignalHandler(ctx context.Context) (context.Context, context.CancelFunc, func()) {
	ctx, cancel := context.WithCancel(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	cleanup := func() {
		signal.Stop(sigCh)
	}

	return ctx, cancel, cleanup
}

// Signals represents a set of OS signals to handle.
type Signals []os.Signal

// DefaultSignals returns the default set of shutdown signals (SIGINT, SIGTERM).
func DefaultSignals() Signals {
	return Signals{syscall.SIGINT, syscall.SIGTERM}
}

// RunWithCustomSignals is like RunWithSignals but allows specifying custom signals.
func RunWithCustomSignals(ctx context.Context, signals Signals, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, signals...)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	return fn(ctx)
}
