package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Transactions that read before they write must not fail when another
// connection commits in between (SQLITE_BUSY_SNAPSHOT), e.g. a re-plan while
// the trailer sync runs.
func TestConcurrentWrites(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ch := &Channel{Number: 1, Name: "Test", Enabled: true, Kind: "manual"}
	if err := st.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stop := time.Now().Add(time.Second)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(stop); i++ {
			tr := &Trailer{Key: fmt.Sprintf("t:%d", i), Origin: OriginDiscover, Title: "x", DurationMs: 1000}
			if err := st.UpsertTrailers(ctx, []*Trailer{tr}); err != nil {
				errs <- fmt.Errorf("trailers: %w", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for time.Now().Before(stop) {
			progs := []Program{{ChannelID: ch.ID, Start: now.UnixMilli(), Stop: now.Add(time.Minute).UnixMilli(), RatingKey: "1"}}
			if err := st.InsertPrograms(ctx, progs); err != nil {
				errs <- fmt.Errorf("insert: %w", err)
				return
			}
			if _, err := st.TakeProgramsAfter(ctx, ch.ID, now); err != nil {
				errs <- fmt.Errorf("take: %w", err)
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
