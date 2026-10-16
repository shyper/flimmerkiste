package store

import (
	"context"
	"testing"
	"time"
)

// Accounts of different media servers live side by side; deleting a user ends
// its sessions.
func TestUsersPerProvider(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := &User{Provider: "plex", ExternalID: "4242", Username: "alex", Role: "admin"}
	if err := st.UpsertUser(ctx, p); err != nil {
		t.Fatal(err)
	}
	j := &User{Provider: "jellyfin", ExternalID: "4242", Username: "other", Role: "viewer"}
	if err := st.UpsertUser(ctx, j); err != nil || j.ID == p.ID {
		t.Fatalf("jellyfin user: %+v %v", j, err)
	}
	st.CreateSession(ctx, "tok2", j.ID, time.Hour)
	if err := st.DeleteUser(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, "tok2"); err == nil {
		t.Error("session of a deleted user survived")
	}
}
