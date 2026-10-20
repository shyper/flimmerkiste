package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

// handleHistory lists what aired, newest first: ?channel=<id>&q=<title>&day=<YYYY-MM-DD>&before=<ms>&limit=<n>.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// what started since the last background run
	if _, err := s.store.RecordHistory(ctx, time.Now()); err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	v := r.URL.Query()
	q := store.HistoryQuery{Search: v.Get("q")}
	q.ChannelID, _ = strconv.ParseInt(v.Get("channel"), 10, 64)
	q.Before, _ = strconv.ParseInt(v.Get("before"), 10, 64)
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	if d := v.Get("day"); d != "" {
		day, err := time.ParseInLocation("2006-01-02", d, time.Local)
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, "api.badRequest")
			return
		}
		q.From, q.To = day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()
	}
	entries, err := s.store.History(ctx, q)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
