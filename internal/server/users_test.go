package server

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

// Another admin can neither demote nor remove the owner.
func TestOwnerStaysAdmin(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner := &store.User{Provider: "plex", ExternalID: "1", Username: "owner", Role: "admin"}
	other := &store.User{Provider: "plex", ExternalID: "2", Username: "other", Role: "admin"}
	for _, u := range []*store.User{owner, other} {
		if err := st.UpsertUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	st.SetSetting(ctx, store.SetOwnerID, "1")
	s := &Server{store: st}

	// as, on behalf of user by, changes the role of (or removes) user id
	as := func(by *store.User, method string, id int64, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "/api/users/x", strings.NewReader(`{"role":"viewer"}`))
		r.SetPathValue("id", strconv.FormatInt(id, 10))
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, by))
		w := httptest.NewRecorder()
		if method == "PUT" {
			s.handlePutUser(w, r)
		} else {
			s.handleDeleteUser(w, r)
		}
		if w.Code != want {
			t.Errorf("%s user %d: status %d, want %d: %s", method, id, w.Code, want, w.Body)
		}
	}
	as(other, "PUT", owner.ID, 400)
	as(other, "DELETE", owner.ID, 400)
	users, _ := st.ListUsers(ctx)
	for _, u := range users {
		if u.ID == owner.ID && u.Role != "admin" {
			t.Errorf("owner demoted to %s", u.Role)
		}
	}
	// everyone else can still be changed
	as(owner, "PUT", other.ID, 200)
}

// Switching shared users off signs everybody but the owner out.
func TestSharedUsersOffEndsSessions(t *testing.T) {
	srv, h := transferWorld(t, "a")
	ctx := context.Background()
	viewer := &store.User{Provider: "plex", ExternalID: "2", Username: "guest", Role: "viewer"}
	srv.store.UpsertUser(ctx, viewer)
	srv.store.CreateSession(ctx, "v", viewer.ID, time.Hour)
	srv.store.CreateAPIToken(ctx, viewer.ID, "guest's assistant", "tv", []string{ScopeRead})
	call(t, h, "PUT", "/api/settings", map[string]any{"allowSharedUsers": false}, nil)
	if _, err := srv.store.SessionUser(ctx, "v"); err == nil {
		t.Error("the viewer is still signed in")
	}
	if _, _, err := srv.store.TokenUser(ctx, "tv"); err == nil {
		t.Error("the viewer's token still works")
	}
	if _, err := srv.store.SessionUser(ctx, "s"); err != nil {
		t.Errorf("the owner was signed out: %v", err)
	}
}
