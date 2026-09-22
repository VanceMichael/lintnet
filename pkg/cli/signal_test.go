package cli

import (
	"context"
	"syscall"
	"testing"
	"time"
)

func TestNotifySignal_firstSignalCancelsContext(t *testing.T) {
	t.Parallel()
	ctx, stop := notifySignal(context.Background())
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the context is not canceled after SIGINT")
	}

	// stop is idempotent and must not close resources or unsubscribe twice.
	stop()
	stop()
}
