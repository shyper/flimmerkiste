package server

// MCP endpoint (/mcp): assistants such as Claude read the guide, browse the
// media server's library and edit channels through tools. A client signs in
// with an API token (Settings → MCP); the token acts as the admin who created
// it, limited to its scopes, and only sees the tools those scopes allow.
// Most tools run the same handlers as the web UI, so checks and errors match.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/rules"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
)

// Token scopes, each unlocking a group of tools.
const (
	ScopeRead    = "read"    // channels, guide, who is watching, history
	ScopeLibrary = "library" // the media server's library: search, titles, filter values, previews
	ScopeWrite   = "write"   // create and change channels, re-plan, sync the library
	ScopeDelete  = "delete"  // delete channels
)

var Scopes = []string{ScopeRead, ScopeLibrary, ScopeWrite, ScopeDelete}

func validScope(s string) bool { return slices.Contains(Scopes, s) }

// mcpSchemas keeps the tool schemas: a server is built for every request.
var mcpSchemas = mcp.NewSchemaCache()

const mcpInstructions = `Flimmerkiste is a virtual live TV station: channels play titles from a Plex, Jellyfin or Emby media server around the clock, and the media server shows them as live TV.

Before you create or change a channel, call get_server_info (rule fields and their operators) and look at an existing channel with get_channel. Check a filter with preview_source and real values (genres, actors, collections) with library_facets before you save it. Changes to what a channel plays apply to the guide after regenerate_channel (update_channel can do it right away).

Channel kinds and their config.source:
- smart: {"type":"rules","rules":<group>}
- manual: {"type":"manual","items":[{"key":"<ratingKey or show key>","type":"movie|show|season|episode","title":"…","season":<n, for type season>}]} – keys from search_library / get_show
- ai: {"type":"ai","ai":{"theme":"<what the channel is about>","batchSize":12,"episodesPerShow":2,"filter":<optional group>}} – needs an AI provider (get_server_info.aiEnabled)
- seasonal: no source; "seasons":[{"id":"x","name":"…","emoji":"🎃","from":"MM-DD","to":"MM-DD","source":<source>}] and an optional "fallback":<source> outside every season. get_seasonal_preset returns a ready calendar.

A rule group: {"match":"all|any|none","rules":[{"field":"genre","op":"is","values":["Horror"]}],"groups":[<group>…]}. "none" excludes. Several values of one rule are alternatives. Examples: decade "1980", watched "false", addedDays lt "30", type "movie|episode".

Other config fields: order "shuffle|fixed", sortBy "title|year|added|release" (fixed order), keepEpisodeOrder, noRepeatHours, align 0|5|10|15|30|60 (start titles on the clock), filler <source of type manual|rules|trailers> with fillerBetween 0-5, previews (trailers of later titles in the breaks), transcode "default|copy|audio|full", credits "default|off|squeeze", watermark {"enabled":true,"tone":"color|gray|white"} (transcode full only), logoStyle "auto" or a style.

Times are in the server's time zone. Speak to the user in their language; titles stay as the library has them.`

// mcpHandler serves the MCP endpoint; every request needs a bearer token.
func (s *Server) mcpHandler() http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		info := auth.TokenInfoFromContext(r.Context())
		if info == nil {
			return nil
		}
		tok, _ := info.Extra["token"].(*store.APIToken)
		u, _ := info.Extra["user"].(*store.User)
		return s.mcpServer(tok, u)
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       s.log,
		// Every request carries a secret token, which a page rebinding a host
		// name cannot know; the check would refuse reverse proxies on the same host.
		DisableLocalhostProtection: true,
	})
	return auth.RequireBearerToken(s.verifyToken, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
}

// tokenFromPath serves /mcp/<token> for clients that cannot send a header
// (the Claude and ChatGPT apps): the URL itself is the secret.
func tokenFromPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+r.PathValue("token"))
		next.ServeHTTP(w, r)
	})
}

func (s *Server) verifyToken(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	tok, u, err := s.store.TokenUser(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return nil, auth.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	return &auth.TokenInfo{UserID: strconv.FormatInt(u.ID, 10), Scopes: tok.Scopes,
		Extra: map[string]any{"token": tok, "user": u}}, nil
}

// mcpTool describes who may use a tool: the token needs scope, and admin
// tools also need the token's user to be an administrator (still, not only
// when the token was made).
type mcpTool struct {
	scope string
	admin bool
}

func (t mcpTool) allowed(tok *store.APIToken, u *store.User) bool {
	return tok.Has(t.scope) && (!t.admin || u.IsAdmin())
}

var (
	readOnly  = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)}
	writing   = &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}
	replacing = &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)}
)

// addTool registers a tool whose answer is sent as JSON text.
func addTool[In any](srv *mcp.Server, ok bool, t *mcp.Tool, h func(ctx context.Context, in In) (any, error)) {
	if !ok {
		return
	}
	mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := h(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
	})
}

// ---- tool inputs ----

type channelID struct {
	ID int64 `json:"id" jsonschema:"channel id (not the number), from list_channels"`
}

type guideIn struct {
	From      string `json:"from,omitempty" jsonschema:"start, RFC 3339 (e.g. 2026-10-22T20:00:00+02:00); default: half an hour ago"`
	Hours     int    `json:"hours,omitempty" jsonschema:"hours from the start, at most the planned horizon (get_server_info horizonHours); default 6, for a single channel everything planned"`
	ChannelID int64  `json:"channelId,omitempty" jsonschema:"only this channel; without hours its whole planned guide"`
}

type historyIn struct {
	ChannelID int64  `json:"channelId,omitempty" jsonschema:"only this channel"`
	Query     string `json:"query,omitempty" jsonschema:"part of a title or show"`
	Day       string `json:"day,omitempty" jsonschema:"a day, YYYY-MM-DD"`
	Before    string `json:"before,omitempty" jsonschema:"only what started before this time (RFC 3339), for the next page"`
	Limit     int    `json:"limit,omitempty" jsonschema:"at most this many entries (default 100)"`
}

type searchIn struct {
	Query  string `json:"query,omitempty" jsonschema:"part of the title of a movie or show; empty lists everything"`
	Type   string `json:"type,omitempty" jsonschema:"movie or show; empty finds both"`
	Offset int    `json:"offset,omitempty" jsonschema:"skip this many movies and shows (next page)"`
}

type keyIn struct {
	Key string `json:"key" jsonschema:"rating key of a movie or episode, from search_library or get_show"`
}

type showIn struct {
	Key string `json:"key" jsonschema:"key of the show, from search_library"`
}

type facetsIn struct {
	Fields []string `json:"fields,omitempty" jsonschema:"fields to list: genre actor director writer collection label studio country contentRating show decade (default: all)"`
	Limit  int      `json:"limit,omitempty" jsonschema:"values per field, most common first (default 50, at most 400)"`
}

type previewIn struct {
	Source map[string]any `json:"source" jsonschema:"a source as in a channel config, e.g. {\"type\":\"rules\",\"rules\":{…}}"`
	SortBy string         `json:"sortBy,omitempty" jsonschema:"title, year, added or release"`
}

type createIn struct {
	Name    string         `json:"name"`
	Kind    string         `json:"kind" jsonschema:"smart, manual, ai or seasonal"`
	Number  int            `json:"number,omitempty" jsonschema:"channel number; default: the next free one"`
	Color   string         `json:"color,omitempty" jsonschema:"accent color, e.g. #e5484d"`
	Enabled *bool          `json:"enabled,omitempty" jsonschema:"default true"`
	Config  map[string]any `json:"config" jsonschema:"the channel definition (see the server instructions)"`
}

type updateIn struct {
	ID      int64          `json:"id" jsonschema:"channel id"`
	Name    string         `json:"name,omitempty"`
	Kind    string         `json:"kind,omitempty"`
	Number  int            `json:"number,omitempty"`
	Color   string         `json:"color,omitempty"`
	Enabled *bool          `json:"enabled,omitempty"`
	Config  map[string]any `json:"config,omitempty" jsonschema:"top-level config fields to replace (e.g. {\"source\":…}); fields left out stay"`
	// Regenerate re-plans the guide when the change affects it.
	Regenerate bool `json:"regenerate,omitempty" jsonschema:"re-plan the guide now if the change affects what airs"`
}

type noInput struct{}

// ---- server ----

func (s *Server) mcpServer(tok *store.APIToken, u *store.User) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "flimmerkiste", Title: "Flimmerkiste", Version: Version},
		&mcp.ServerOptions{Instructions: mcpInstructions, SchemaCache: mcpSchemas, Logger: s.log})
	can := func(scope string, admin bool) bool { return mcpTool{scope, admin}.allowed(tok, u) }
	call := func(ctx context.Context, h http.HandlerFunc, method, target string, body any, path ...string) (json.RawMessage, error) {
		return s.callAPI(ctx, u, h, method, target, body, path...)
	}

	// --- read ---
	addTool(srv, can(ScopeRead, false), &mcp.Tool{Name: "get_server_info", Annotations: readOnly,
		Description: "Server name, media server, version, whether AI channels are available, how far ahead the guide is planned, and the rule fields with their operators."},
		func(ctx context.Context, _ noInput) (any, error) { return s.mcpInfo(ctx), nil })
	addTool(srv, can(ScopeRead, false), &mcp.Tool{Name: "list_channels", Annotations: readOnly,
		Description: "All channels with id, number, name, kind, whether enabled, what is on now and next, planning problems and whether a channel is being planned right now."},
		func(ctx context.Context, _ noInput) (any, error) { return s.mcpChannels(ctx) })
	addTool(srv, can(ScopeRead, false), &mcp.Tool{Name: "get_channel", Annotations: readOnly,
		Description: "One channel with its full config (source, rules, seasons, filler …); manual entries whose titles are gone are listed under missing."},
		func(ctx context.Context, in channelID) (any, error) {
			return call(ctx, s.handleGetChannel, "GET", "/api/channels/x", nil, "id", strconv.FormatInt(in.ID, 10))
		})
	addTool(srv, can(ScopeRead, false), &mcp.Tool{Name: "get_guide", Annotations: readOnly,
		Description: "The program guide: what airs when on the enabled channels. All channels: 6 hours by default. One channel (channelId) without hours: its whole planned guide."},
		func(ctx context.Context, in guideIn) (any, error) { return s.mcpGuide(ctx, in) })
	addTool(srv, can(ScopeRead, false), &mcp.Tool{Name: "get_sessions", Annotations: readOnly,
		Description: "Who is watching which channel right now."},
		func(ctx context.Context, _ noInput) (any, error) { return s.streamer.Sessions(), nil })
	addTool(srv, can(ScopeRead, true), &mcp.Tool{Name: "get_history", Annotations: readOnly,
		Description: "Movies and episodes that aired, newest first, with whether someone watched (watched) and how often the title aired."},
		func(ctx context.Context, in historyIn) (any, error) {
			q := url.Values{}
			if in.ChannelID != 0 {
				q.Set("channel", strconv.FormatInt(in.ChannelID, 10))
			}
			if in.Query != "" {
				q.Set("q", in.Query)
			}
			if in.Day != "" {
				q.Set("day", in.Day)
			}
			if in.Before != "" {
				t, err := time.Parse(time.RFC3339, in.Before)
				if err != nil {
					return nil, fmt.Errorf("before: %w", err)
				}
				q.Set("before", strconv.FormatInt(t.UnixMilli(), 10))
			}
			if in.Limit > 0 {
				q.Set("limit", strconv.Itoa(in.Limit))
			}
			return call(ctx, s.handleHistory, "GET", "/api/history?"+q.Encode(), nil)
		})
	addTool(srv, can(ScopeRead, true), &mcp.Tool{Name: "get_seasonal_preset", Annotations: readOnly,
		Description: "A ready-made calendar (Christmas, winter, Easter, summer, Halloween …) to start the config of a seasonal channel from."},
		func(ctx context.Context, _ noInput) (any, error) {
			return call(ctx, s.handleSeasonalPreset, "GET", "/api/presets/seasonal", nil)
		})

	// --- library (the media server) ---
	addTool(srv, can(ScopeLibrary, true), &mcp.Tool{Name: "list_libraries", Annotations: readOnly,
		Description: "The media server's libraries, whether each is used, and the state of the library sync."},
		func(ctx context.Context, _ noInput) (any, error) {
			return call(ctx, s.handleLibraries, "GET", "/api/libraries", nil)
		})
	addTool(srv, can(ScopeLibrary, true), &mcp.Tool{Name: "search_library", Annotations: readOnly,
		Description: "Search movies and shows of the media server by title (60 of each per page; moreMovies/moreShows tell whether there are more)."},
		func(ctx context.Context, in searchIn) (any, error) {
			q := url.Values{"q": {in.Query}, "type": {in.Type}, "offset": {strconv.Itoa(in.Offset)}}
			return call(ctx, s.handleSearch, "GET", "/api/library/search?"+q.Encode(), nil)
		})
	addTool(srv, can(ScopeLibrary, true), &mcp.Tool{Name: "get_title", Annotations: readOnly,
		Description: "Everything about one movie or episode: summary, genres, cast, director, collections, labels, rating, duration, whether watched."},
		func(ctx context.Context, in keyIn) (any, error) {
			m := s.lib.Get(ctx, in.Key)
			if m == nil {
				return nil, errors.New(i18n.T(s.lang(ctx), "api.titleNotFound"))
			}
			return m, nil
		})
	addTool(srv, can(ScopeLibrary, true), &mcp.Tool{Name: "get_show", Annotations: readOnly,
		Description: "The episodes of a show in the library, by season."},
		func(ctx context.Context, in showIn) (any, error) {
			return call(ctx, s.handleShow, "GET", "/api/library/show/x", nil, "key", in.Key)
		})
	addTool(srv, can(ScopeLibrary, true), &mcp.Tool{Name: "library_facets", Annotations: readOnly,
		Description: "The values found in the library for rule fields (genres, actors, collections …) with how many titles have each; use them in rules."},
		func(ctx context.Context, in facetsIn) (any, error) {
			items, err := s.lib.All(ctx)
			if err != nil {
				return nil, err
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			all := rules.Facets(items, min(limit, 400))
			if len(in.Fields) == 0 {
				return all, nil
			}
			out := map[string][]rules.FacetValue{}
			for _, f := range in.Fields {
				out[f] = all[f]
			}
			return out, nil
		})
	addTool(srv, can(ScopeLibrary, true), &mcp.Tool{Name: "preview_source", Annotations: readOnly,
		Description: "Which titles a source (rules, manual list or trailers) finds: counts, total duration and up to 60 examples. Use it to check a filter before saving a channel."},
		func(ctx context.Context, in previewIn) (any, error) {
			return call(ctx, s.handlePreview, "POST", "/api/preview", map[string]any{"source": in.Source, "sortBy": in.SortBy})
		})

	// --- write ---
	addTool(srv, can(ScopeWrite, true), &mcp.Tool{Name: "create_channel", Annotations: writing,
		Description: "Create a channel and plan its guide. Leave out what the web editor would default: order shuffle, keepEpisodeOrder, noRepeatHours 48, transcode default, logoStyle auto."},
		func(ctx context.Context, in createIn) (any, error) {
			cfg := map[string]any{"order": "shuffle", "keepEpisodeOrder": true, "noRepeatHours": 48, "transcode": "default", "logoStyle": "auto"}
			if in.Kind == schedule.KindManual {
				cfg["order"], cfg["noRepeatHours"] = "fixed", 0
			}
			for k, v := range in.Config {
				cfg[k] = v
			}
			return call(ctx, s.handleCreateChannel, "POST", "/api/channels",
				map[string]any{"name": in.Name, "kind": in.Kind, "number": in.Number, "color": in.Color, "enabled": in.Enabled, "config": cfg})
		})
	addTool(srv, can(ScopeWrite, true), &mcp.Tool{Name: "update_channel", Annotations: replacing,
		Description: "Change a channel: only the fields given change; config fields replace the stored ones of the same name. planChanged in the answer says whether the guide needs re-planning (regenerate does it right away)."},
		func(ctx context.Context, in updateIn) (any, error) { return s.mcpUpdate(ctx, u, in) })
	addTool(srv, can(ScopeWrite, true), &mcp.Tool{Name: "regenerate_channel", Annotations: writing,
		Description: "Re-plan the future guide of a channel in the background (AI channels can take a few minutes; get_channel shows errors)."},
		func(ctx context.Context, in channelID) (any, error) {
			return call(ctx, s.handleRegenerate, "POST", "/api/channels/x/regenerate", nil, "id", strconv.FormatInt(in.ID, 10))
		})
	addTool(srv, can(ScopeWrite, true), &mcp.Tool{Name: "regenerate_all", Annotations: writing,
		Description: "Re-plan every enabled channel, one after another in the background."},
		func(ctx context.Context, _ noInput) (any, error) {
			return call(ctx, s.handleRegenerateAll, "POST", "/api/channels/regenerate", nil)
		})
	addTool(srv, can(ScopeWrite, true), &mcp.Tool{Name: "sync_library", Annotations: writing,
		Description: "Read the media server's libraries again now (new and removed titles) instead of waiting for the next scheduled sync."},
		func(ctx context.Context, _ noInput) (any, error) {
			return call(ctx, s.handleSync, "POST", "/api/library/sync", nil)
		})

	// --- delete ---
	addTool(srv, can(ScopeDelete, true), &mcp.Tool{Name: "delete_channel", Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
		Description: "Delete a channel with its guide and logo. This cannot be undone; ask the user first."},
		func(ctx context.Context, in channelID) (any, error) {
			return call(ctx, s.handleDeleteChannel, "DELETE", "/api/channels/x", nil, "id", strconv.FormatInt(in.ID, 10))
		})
	return srv
}

// callAPI runs a web API handler as u and returns its JSON answer; an error
// answer becomes an error with its (translated) message.
func (s *Server) callAPI(ctx context.Context, u *store.User, h http.HandlerFunc, method, target string, body any, path ...string) (json.RawMessage, error) {
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	r, err := http.NewRequestWithContext(context.WithValue(ctx, ctxKey{}, u), method, target, rd)
	if err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(path); i += 2 {
		r.SetPathValue(path[i], path[i+1])
	}
	w := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	h(w, r)
	if w.status >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(w.body.Bytes(), &e) == nil && e.Error != "" {
			return nil, errors.New(e.Error)
		}
		return nil, fmt.Errorf("%s %s: status %d", method, target, w.status)
	}
	return json.RawMessage(bytes.TrimSpace(w.body.Bytes())), nil
}

// bufferedResponse collects what a handler writes.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(status int)      { b.status = status }

// ---- tools of their own: compact answers for a model ----

func (s *Server) mcpInfo(ctx context.Context) map[string]any {
	l := s.lang(ctx)
	type field struct {
		ID   string   `json:"id"`
		Kind string   `json:"kind"`
		Ops  []string `json:"ops"`
		Unit string   `json:"unit,omitempty"`
	}
	fields := make([]field, len(rules.Fields))
	for i, f := range rules.Fields {
		fields[i] = field{ID: f.ID, Kind: f.Kind, Ops: f.Ops, Unit: f.Unit}
	}
	return map[string]any{
		"serverName":   s.store.Setting(ctx, store.SetServerName, ""),
		"mediaServer":  platformName(s.platform(ctx)),
		"version":      Version,
		"language":     l,
		"timeZone":     time.Now().Format("MST -07:00"),
		"now":          time.Now().Format(time.RFC3339),
		"aiEnabled":    s.store.Setting(ctx, store.SetAIProvider, "") != "",
		"horizonHours": s.cfg.Horizon,
		"channelKinds": []string{schedule.KindSmart, schedule.KindManual, schedule.KindAI, schedule.KindSeasonal},
		"ruleFields":   fields,
	}
}

type mcpProgram struct {
	Start    string `json:"start"`
	Stop     string `json:"stop"`
	Key      string `json:"key,omitempty"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Year     int    `json:"year,omitempty"`
	Note     string `json:"note,omitempty"` // e.g. why the AI picked it
}

func mcpProgramOf(start, stop int64, note string, v mediaView) mcpProgram {
	return mcpProgram{Start: time.UnixMilli(start).Format(time.RFC3339), Stop: time.UnixMilli(stop).Format(time.RFC3339),
		Key: v.Key, Title: v.Title, Subtitle: v.Subtitle, Year: v.Year, Note: note}
}

func (s *Server) mcpChannels(ctx context.Context) (any, error) {
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	type row struct {
		ID       int64       `json:"id"`
		Number   int         `json:"number"`
		Name     string      `json:"name"`
		Kind     string      `json:"kind"`
		Enabled  bool        `json:"enabled"`
		Now      *mcpProgram `json:"now,omitempty"`
		Next     *mcpProgram `json:"next,omitempty"`
		Error    string      `json:"error,omitempty"`
		Planning bool        `json:"planning,omitempty"`
	}
	out := []row{}
	for _, c := range chans {
		v := s.channelView(ctx, c, c.Enabled)
		rw := row{ID: c.ID, Number: c.Number, Name: c.Name, Kind: c.Kind, Enabled: c.Enabled, Error: v.Error, Planning: v.Planning}
		if v.Now != nil {
			p := mcpProgramOf(v.Now.Start, v.Now.Stop, v.Now.Note, v.Now.Media)
			rw.Now = &p
		}
		if v.Next != nil {
			p := mcpProgramOf(v.Next.Start, v.Next.Stop, v.Next.Note, v.Next.Media)
			rw.Next = &p
		}
		out = append(out, rw)
	}
	return out, nil
}

func (s *Server) mcpGuide(ctx context.Context, in guideIn) (any, error) {
	from := time.Now().Add(-30 * time.Minute)
	if in.From != "" {
		t, err := time.Parse(time.RFC3339, in.From)
		if err != nil {
			return nil, fmt.Errorf("from: %w", err)
		}
		from = t
	}
	// never less than 48 hours, as before the horizon became the limit
	horizon := time.Duration(max(s.cfg.Horizon, 48)) * time.Hour
	var to time.Time
	switch {
	case in.Hours > 0:
		to = from.Add(min(time.Duration(in.Hours)*time.Hour, horizon))
	case in.ChannelID != 0:
		// one channel without hours: everything planned
		to = time.Now().Add(horizon)
		if from.After(time.Now()) {
			to = from.Add(horizon)
		}
	default:
		to = from.Add(6 * time.Hour)
	}
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	type row struct {
		ID       int64        `json:"id"`
		Number   int          `json:"number"`
		Name     string       `json:"name"`
		Programs []mcpProgram `json:"programs"`
	}
	out := []row{}
	for _, c := range chans {
		if !c.Enabled || (in.ChannelID != 0 && c.ID != in.ChannelID) {
			continue
		}
		rw := row{ID: c.ID, Number: c.Number, Name: c.Name, Programs: []mcpProgram{}}
		if s.fillerOnly(c) {
			for _, p := range noInfoViews(s.lang(ctx), from, to) {
				rw.Programs = append(rw.Programs, mcpProgramOf(p.Start, p.Stop, "", p.Media))
			}
		} else {
			for _, p := range s.mainPrograms(ctx, c.ID, from, to) {
				if m := s.lib.Get(ctx, p.RatingKey); m != nil {
					rw.Programs = append(rw.Programs, mcpProgramOf(p.Start, p.Stop, p.Note, viewOf(m, false)))
				}
			}
		}
		out = append(out, rw)
	}
	return out, nil
}

// mcpUpdate changes only the given fields of a channel, the web editor
// always sends all of them.
func (s *Server) mcpUpdate(ctx context.Context, u *store.User, in updateIn) (any, error) {
	c, err := s.store.GetChannel(ctx, in.ID)
	if err != nil {
		return nil, errors.New(i18n.T(s.lang(ctx), "api.channelNotFound"))
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(c.Config, &cfg); err != nil {
		return nil, err
	}
	for k, v := range in.Config {
		cfg[k] = v
	}
	body := map[string]any{"name": c.Name, "kind": c.Kind, "number": c.Number, "color": c.Color, "enabled": c.Enabled, "config": cfg}
	if in.Name != "" {
		body["name"] = in.Name
	}
	if in.Kind != "" {
		body["kind"] = in.Kind
	}
	if in.Number > 0 {
		body["number"] = in.Number
	}
	if in.Color != "" {
		body["color"] = in.Color
	}
	if in.Enabled != nil {
		body["enabled"] = *in.Enabled
	}
	id := strconv.FormatInt(c.ID, 10)
	raw, err := s.callAPI(ctx, u, s.handleUpdateChannel, "PUT", "/api/channels/x", body, "id", id)
	if err != nil {
		return nil, err
	}
	var res struct {
		PlanChanged bool `json:"planChanged"`
	}
	_ = json.Unmarshal(raw, &res)
	if in.Regenerate && res.PlanChanged {
		s.regenerate(c.ID)
		return map[string]any{"channel": raw, "planChanged": true, "regenerating": true}, nil
	}
	return raw, nil
}

// ---- token management (web UI) ----

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListAPITokens(r.Context())
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens, "scopes": Scopes, "endpoint": s.baseURL(r) + "/mcp"})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := readJSON(r, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "api.badRequest")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		s.fail(w, r, http.StatusBadRequest, "api.nameMissing")
		return
	}
	for _, want := range in.Scopes {
		if !validScope(want) {
			s.fail(w, r, http.StatusBadRequest, "api.badScope")
			return
		}
	}
	var scopes []string
	for _, sc := range Scopes { // in a fixed order, each once
		if slices.Contains(in.Scopes, sc) {
			scopes = append(scopes, sc)
		}
	}
	if len(scopes) == 0 {
		s.fail(w, r, http.StatusBadRequest, "api.noScope")
		return
	}
	token := "fk_" + randomHex(32)
	t, err := s.store.CreateAPIToken(r.Context(), userFrom(r).ID, in.Name, token, scopes)
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	t.Username = userFrom(r).Username
	// The token itself is only ever shown in this answer.
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "info": t})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.store.DeleteAPIToken(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, http.StatusNotFound, "api.tokenNotFound")
		return
	} else if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
