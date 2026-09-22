package cli

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// exitCodeCanceled is the process exit code after the command is canceled by a signal.
// 128 + SIGINT(2) = 130.
const exitCodeCanceled = 130

// notifySignal subscribes to SIGINT and SIGTERM.
//
// The first signal cancels the returned context so that running commands
// enter the cancellation lifecycle. A second signal forces the process to exit.
//
// The returned stop function unsubscribes the signals and stops the watcher goroutine.
// It is safe to call stop multiple times; the signal subscription is registered
// exactly once and removed exactly once.
func notifySignal(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-done:
			return
		case <-ch:
		}
		// The first signal requests graceful cancellation.
		cancel()
		select {
		case <-done:
			return
		case <-ch:
			// The second signal forces the process to exit.
			os.Exit(exitCodeCanceled)
		}
	}()
	stop := func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
			cancel()
		})
	}
	return ctx, stop
}
