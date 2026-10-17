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

// The catalog knows the years the episodes of a show span.
func TestShowYears(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	st.UpsertLibrary(ctx, Library{ID: "s", Title: "Serien", Type: "show"})
	ep := func(key string, year int) *Media {
		return &Media{RatingKey: key, LibraryID: "s", Type: "episode", Title: key, ShowKey: "sh", Year: year, DurationMs: 1}
	}
	st.UpsertMedia(ctx, []*Media{ep("a", 1994), ep("b", 1987), ep("c", 0), ep("d", 2001)}, 1)
	all, err := st.AllMedia(ctx)
	if err != nil || len(all) != 4 {
		t.Fatalf("%d items, %v", len(all), err)
	}
	for _, m := range all {
		if m.ShowFirstYear != 1987 || m.ShowLastYear != 2001 {
			t.Errorf("%s: %d–%d", m.RatingKey, m.ShowFirstYear, m.ShowLastYear)
		}
	}
}
