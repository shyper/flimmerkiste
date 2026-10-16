package schedule

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/store"
)

// A stream does not wait for a channel that is being planned (an AI request
// may take minutes): EnsureNow and BindPreviewsNow give up after busyWait.
func TestStreamDoesNotWaitForPlanning(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(st, library.New(st, nil, log), nil, time.Hour, log)

	l := s.lock(1)
	l.Lock() // planning in progress
	started := time.Now()
	if err := s.EnsureNow(ctx, 1); !errors.Is(err, ErrBusy) {
		t.Errorf("EnsureNow while planning: %v", err)
	}
	if err := s.BindPreviewsNow(ctx, 1, time.Now()); !errors.Is(err, ErrBusy) {
		t.Errorf("BindPreviewsNow while planning: %v", err)
	}
	if d := time.Since(started); d > 3*busyWait {
		t.Errorf("waited %v", d)
	}

	// planning that ends within busyWait is waited for
	go func() {
		time.Sleep(busyWait / 4)
		l.Unlock()
	}()
	if err := s.EnsureNow(ctx, 1); errors.Is(err, ErrBusy) {
		t.Error("did not wait for a short planning run")
	}
}
