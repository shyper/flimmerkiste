package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/shyper/flimmerkiste/internal/ai"
	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/library"
	"github.com/shyper/flimmerkiste/internal/rules"
	"github.com/shyper/flimmerkiste/internal/store"
)

var ErrNoContent = i18n.E("schedule.noContent")

// ErrNoFiller: a channel without titles of its own found no trailers or clips.
var ErrNoFiller = i18n.E("schedule.noFiller")

// AIFunc returns the configured AI provider (or ai.ErrDisabled).
type AIFunc func(ctx context.Context) (ai.Provider, error)

type Scheduler struct {
	store   *store.Store
	lib     *library.Library
	ai      AIFunc
	log     *slog.Logger
	horizon time.Duration

	// OnChange is called after the guide of a channel changed. extended is true
	// when programs were only appended to the end of a running guide.
	OnChange func(extended bool)

	mu    sync.Mutex
	locks map[int64]*sync.Mutex
	errs  map[int64]error // last generation problem per channel, shown in the UI
}

func New(st *store.Store, lib *library.Library, aiFn AIFunc, horizon time.Duration, log *slog.Logger) *Scheduler {
	return &Scheduler{store: st, lib: lib, ai: aiFn, log: log, horizon: horizon,
		locks: map[int64]*sync.Mutex{}, errs: map[int64]error{}}
}

func (s *Scheduler) lock(id int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	return l
}

func (s *Scheduler) setErr(id int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.errs, id)
	} else {
		s.errs[id] = err
	}
}

func (s *Scheduler) changed(extended bool) {
	if s.OnChange != nil {
		s.OnChange(extended)
	}
}

// lang is the language of the installation (for AI reasons and season names).
func (s *Scheduler) lang(ctx context.Context) i18n.Lang {
	if s.store == nil {
		return i18n.EN
	}
	if v := s.store.Setting(ctx, store.SetLanguage, ""); i18n.Known(v) {
		return i18n.Lang(v)
	}
	return i18n.EN
}

// aiCandidates is how many titles one AI request offers at most.
func (s *Scheduler) aiCandidates(ctx context.Context) int {
	if s.store == nil {
		return DefaultAICandidates
	}
	return min(max(s.store.SettingInt(ctx, store.SetAICandidates, DefaultAICandidates), MinAICandidates), MaxAICandidates)
}

// LastError returns the last generation problem of a channel (nil if fine).
func (s *Scheduler) LastError(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errs[id]
}

// ClearError forgets the last generation problem of a channel (dismissed in
// the UI). It comes back if the next run fails again.
func (s *Scheduler) ClearError(id int64) {
	s.setErr(id, nil)
}

// Run keeps all guides filled up to the horizon. It wakes up rarely; idle cost is negligible.
func (s *Scheduler) Run(ctx context.Context) {
	s.EnsureAll(ctx)
	s.BindAllPreviews(ctx)
	t := time.NewTicker(20 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.EnsureAll(ctx)
			s.BindAllPreviews(ctx)
			_ = s.store.PurgeProgramsBefore(ctx, time.Now().Add(-24*time.Hour))
		}
	}
}

func (s *Scheduler) EnsureAll(ctx context.Context) {
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		s.log.Error("list channels", "err", err)
		return
	}
	for _, ch := range chans {
		if !ch.Enabled {
			continue
		}
		if err := s.Ensure(ctx, ch.ID); err != nil {
			s.log.Warn("schedule channel", "channel", ch.Name, "err", err)
		}
	}
}

// Reset discards the future guide of a channel (keeping what airs right now) and regenerates it.
// AI sources drop their queued picks too, so the model is asked again.
func (s *Scheduler) Reset(ctx context.Context, channelID int64) error {
	return s.replan(ctx, channelID, time.Now(), true)
}

// Replan discards all programs starting at or after from and plans them again.
// The scheduler state is rolled back first, so fixed orders continue exactly
// where the kept guide ends, discarded shuffle picks count as unplayed and
// AI picks go back into the queue instead of being requested again.
func (s *Scheduler) Replan(ctx context.Context, channelID int64, from time.Time) error {
	return s.replan(ctx, channelID, from, false)
}

func (s *Scheduler) replan(ctx context.Context, channelID int64, from time.Time, freshAI bool) error {
	l := s.lock(channelID)
	l.Lock()
	err := s.discard(ctx, channelID, from, freshAI)
	l.Unlock()
	if err != nil {
		return err
	}
	return s.Ensure(ctx, channelID)
}

// discard removes the programs from from on and rolls the state back. With
// freshAI the discarded AI picks are not requeued and every AI queue is emptied.
func (s *Scheduler) discard(ctx context.Context, channelID int64, from time.Time, freshAI bool) error {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	cfg, err := ParseConfig(ch.Config)
	if err != nil {
		return err
	}
	taken, err := s.store.TakeProgramsAfter(ctx, channelID, from)
	if err != nil || (len(taken) == 0 && !freshAI) {
		return err
	}
	if len(taken) > 0 {
		s.changed(false)
	}
	state := &State{}
	_ = json.Unmarshal(ch.State, state)
	state.init()
	if freshAI {
		for id, ss := range state.Sources {
			if src := cfg.SourceByID(id); src != nil && src.Type == "ai" {
				ss.Queue = nil
			}
		}
	}
	requeue := map[string][]QueuedPick{}
	for _, p := range taken {
		if !p.IsMain() || p.SourceID == "" {
			continue
		}
		ss := state.source(p.SourceID)
		if ss.Resume == "" {
			ss.Resume = p.RatingKey
		}
		ss.Played = without(ss.Played, p.RatingKey)
		src := cfg.SourceByID(p.SourceID)
		if m := s.lib.Get(ctx, p.RatingKey); m != nil && m.Type == "episode" {
			ss.Played = without(ss.Played, "show:"+m.ShowKey)
			if src == nil || src.Type != "ai" {
				if ss.ShowResume == nil {
					ss.ShowResume = map[string]string{}
				}
				if _, ok := ss.ShowResume[m.ShowKey]; !ok {
					ss.ShowResume[m.ShowKey] = p.RatingKey
				}
			}
		}
		if src != nil && src.Type == "ai" && !freshAI {
			requeue[p.SourceID] = append(requeue[p.SourceID], QueuedPick{Key: p.RatingKey, Reason: p.Note})
		}
	}
	for id, picks := range requeue {
		ss := state.source(id)
		ss.Queue = append(picks, ss.Queue...)
	}
	// Point the cursors at the last program that is kept, so a resume target that
	// no longer exists (deleted in Plex) continues right after the kept guide.
	kept, err := s.store.ProgramsBetween(ctx, channelID, from.Add(-14*24*time.Hour), from)
	if err != nil {
		return err
	}
	lastKey := map[string]string{}
	lastEp := map[string]map[string]string{}
	for _, p := range kept {
		if !p.IsMain() || p.SourceID == "" || p.Start >= from.UnixMilli() {
			continue
		}
		lastKey[p.SourceID] = p.RatingKey
		if m := s.lib.Get(ctx, p.RatingKey); m != nil && m.Type == "episode" {
			if lastEp[p.SourceID] == nil {
				lastEp[p.SourceID] = map[string]string{}
			}
			lastEp[p.SourceID][m.ShowKey] = p.RatingKey
		}
	}
	for id, ss := range state.Sources {
		if k, ok := lastKey[id]; ok && ss.Resume != "" {
			ss.Last = k
		}
		if ss.ShowCursor == nil {
			ss.ShowCursor = map[string]string{}
		}
		for show := range ss.ShowResume {
			if k, ok := lastEp[id][show]; ok {
				ss.ShowCursor[show] = k
			}
		}
	}
	raw, _ := json.Marshal(state)
	return s.store.SaveChannelState(ctx, channelID, raw)
}

func without(list []string, v string) []string {
	out := list[:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// TrailerLanguages are the preferred audio languages, which trailers have to match.
func (s *Scheduler) TrailerLanguages(ctx context.Context) []string {
	var out []string
	for _, l := range strings.Split(s.store.Setting(ctx, store.SetAudioLanguages, "deu,ger"), ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Ensure fills the guide of one channel up to now+horizon.
func (s *Scheduler) Ensure(ctx context.Context, channelID int64) error {
	l := s.lock(channelID)
	l.Lock()
	defer l.Unlock()
	err := s.ensure(ctx, channelID)
	s.setErr(channelID, err)
	return err
}

// ErrBusy: the channel is being planned right now.
var ErrBusy = errors.New("channel is being planned")

// busyWait is how long a stream waits for a channel that is being planned:
// long enough for ordinary channels, short against AI requests.
const busyWait = 2 * time.Second

// tryLock takes l if it becomes free within busyWait.
func tryLock(ctx context.Context, l *sync.Mutex) bool {
	deadline := time.Now().Add(busyWait)
	for !l.TryLock() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}

// EnsureNow is Ensure for a running stream: while the channel is being planned
// (AI requests take minutes) it returns ErrBusy instead of waiting, so the
// stream can fill the time.
func (s *Scheduler) EnsureNow(ctx context.Context, channelID int64) error {
	l := s.lock(channelID)
	if !tryLock(ctx, l) {
		return ErrBusy
	}
	defer l.Unlock()
	err := s.ensure(ctx, channelID)
	s.setErr(channelID, err)
	return err
}

func (s *Scheduler) ensure(ctx context.Context, channelID int64) error {
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	cfg, err := ParseConfig(ch.Config)
	if err != nil {
		return i18n.E("api.badConfig", err)
	}
	// Programme timestamps use milliseconds. Match their precision so a guide
	// ending exactly at half the horizon does not get an extra fallback title.
	now := time.Now().Truncate(time.Millisecond)
	until := now.Add(s.horizon)
	cursor := now
	last, lastErr := s.store.LastProgram(ctx, channelID)
	appending := lastErr == nil && last.StopTime().After(now)
	if appending {
		cursor = last.StopTime()
	} else if cfg.Align > 0 {
		// Nothing on air (new channel, or the guide ran out): start at the last
		// round time, as if the channel had been running. Created at 20:05, the
		// first title starts at 20:00 and the viewer tunes in at minute 5.
		cursor = alignDown(now, time.Duration(cfg.Align)*time.Minute)
		if lastErr == nil && last.StopTime().After(cursor) {
			cursor = last.StopTime() // the old guide ended in between
		}
	}
	if !cursor.Before(until) {
		return nil
	}
	catalog, err := s.lib.All(ctx)
	if err != nil {
		return err
	}
	if len(catalog) == 0 {
		return i18n.E("schedule.emptyLibrary")
	}

	state := &State{}
	_ = json.Unmarshal(ch.State, state)
	state.init()

	g := &generator{
		s: s, ctx: ctx, ch: ch, cfg: cfg, state: state, catalog: catalog,
		aiFallbackUntil: now.Add(s.horizon / 2),
		rng:             rand.New(rand.NewSource(time.Now().UnixNano())),
		resolved:        map[string][]*store.Media{},
		langs:           s.TrailerLanguages(ctx),
		overrun:         fillerOverrun,
	}
	g.index()
	window := time.Duration(cfg.NoRepeatHours) * time.Hour
	g.lastAired = map[string]time.Time{}
	if window > 0 {
		recent, err := s.store.ProgramsBetween(ctx, channelID, now.Add(-window), until)
		if err == nil {
			for _, p := range recent {
				g.lastAired[p.RatingKey] = p.StartTime()
			}
		}
	}

	var progs []store.Program
	var genErr error
	add := func(p store.Program) {
		p.ChannelID = channelID
		progs = append(progs, p)
		cursor = time.UnixMilli(p.Stop)
	}
	fillerOnly := cfg.FillerOnly(ch.Kind)
	if fillerOnly {
		// No titles of its own: clips back to back.
		for cursor.Before(until) && len(progs) < 4000 {
			f := g.pickFiller(cursor, time.Hour)
			if f == nil {
				genErr = ErrNoFiller
				break
			}
			for _, p := range fillerPrograms(f, cursor, until.Add(time.Hour)) {
				add(p)
			}
		}
	}
	// After a re-plan the current program is kept, a title or a clip of a break:
	// fill up to the next round start time (nothing to do if the guide ends on one).
	if cfg.Align > 0 && !fillerOnly {
		for _, p := range g.fillGap(cursor, nextStart(cursor, time.Duration(cfg.Align)*time.Minute)) {
			add(p)
		}
	}
	for !fillerOnly && cursor.Before(until) && len(progs) < 4000 {
		item, srcID, src, note, err := g.next(cursor)
		if err != nil {
			genErr = err
			break
		}
		stop := cursor.Add(time.Duration(item.DurationMs) * time.Millisecond)
		g.lastAired[item.RatingKey] = cursor
		add(store.Program{Start: cursor.UnixMilli(), Stop: stop.UnixMilli(), RatingKey: item.RatingKey, Source: src, Note: note, SourceID: srcID})

		// Clips between titles, then fill up to the next round start time.
		for i := 0; i < cfg.FillerBetween; i++ {
			f := g.pickFiller(cursor, 10*time.Minute)
			if f == nil {
				break
			}
			for _, p := range fillerPrograms(f, cursor, cursor.Add(time.Hour)) {
				add(p)
			}
		}
		if cfg.Align > 0 {
			for _, p := range g.fillGap(cursor, nextStart(cursor, time.Duration(cfg.Align)*time.Minute)) {
				add(p)
			}
		}
	}
	if err := s.store.InsertPrograms(ctx, progs); err != nil {
		return err
	}
	raw, _ := json.Marshal(state)
	if err := s.store.SaveChannelState(ctx, channelID, raw); err != nil {
		return err
	}
	if len(progs) > 0 {
		s.changed(appending)
		s.log.Debug("scheduled", "channel", ch.Name, "programs", len(progs))
	}
	if genErr != nil {
		return genErr
	}
	return g.softErr
}

// alignDown rounds t down to a multiple of step in local time.
func alignDown(t time.Time, step time.Duration) time.Time {
	_, offset := t.Zone()
	local := t.Add(time.Duration(offset) * time.Second)
	return local.Truncate(step).Add(-time.Duration(offset) * time.Second)
}

// alignUp rounds t up to the next multiple of step in local time.
func alignUp(t time.Time, step time.Duration) time.Time {
	_, offset := t.Zone()
	local := t.Add(time.Duration(offset) * time.Second)
	r := local.Truncate(step)
	if r.Before(local) {
		r = r.Add(step)
	}
	return r.Add(-time.Duration(offset) * time.Second)
}

// alignSlack: a title ending less than this after a round time is treated as
// on time; the next one follows directly instead of waiting for the next slot.
const alignSlack = time.Minute

// nextStart returns when the next title starts on a channel aligned to step.
func nextStart(t time.Time, step time.Duration) time.Time {
	if t.Sub(alignDown(t, step)) < alignSlack {
		return t
	}
	return alignUp(t, step)
}

// fillerOverrun: how far the last clip of a break may run past the round start time.
const fillerOverrun = 45 * time.Second

// minCard is the shortest gap worth trying to fill with a filler clip.
const minCard = 2 * time.Second

// Preview slots (Config.Previews) take the end of a break when it is long enough.
const (
	previewMin = 90 * time.Second
	previewMax = 4 * time.Minute
)

// fillGap fills [from, to) with filler clips that fit and an "up next" slate for
// the rest; with previews enabled, the end of the break becomes a preview slot.
func (g *generator) fillGap(from, to time.Time) []store.Program {
	var out []store.Program
	clipsUntil := to
	if g.cfg.Previews && to.Sub(from) >= previewMin {
		clipsUntil = to.Add(-min(to.Sub(from), previewMax))
	}
	cursor := from
	for i := 0; i < 50 && clipsUntil.Sub(cursor) >= minCard; i++ {
		f := g.pickFiller(cursor, clipsUntil.Sub(cursor))
		if f == nil {
			break
		}
		ps := fillerPrograms(f, cursor, clipsUntil)
		out = append(out, ps...)
		cursor = time.UnixMilli(ps[len(ps)-1].Stop)
	}
	if to.UnixMilli() <= cursor.UnixMilli() {
		return out
	}
	if g.overrun > 0 && clipsUntil.Equal(to) && to.Sub(cursor) >= minCard {
		// One more clip covering the rest, running at most overrun past the round
		// time: the next title starts a little late instead of after a long slate.
		if f := g.pickFillerBetween(cursor, to.Sub(cursor), to.Sub(cursor)+g.overrun); f != nil {
			return append(out, fillerPrograms(f, cursor, to.Add(g.overrun))...)
		}
	}
	// Whatever is left goes to the slate, however short, so the next title starts exactly on time.
	p := store.Program{Start: cursor.UnixMilli(), Stop: to.UnixMilli(), RatingKey: store.CardKey, Kind: store.KindCard}
	if clipsUntil.Before(to) {
		p.RatingKey, p.Kind = store.PreviewKey, store.KindPreview
	}
	return append(out, p)
}

// promoLen is the slate after a trailer naming when the advertised title airs or opens.
const promoLen = 6 * time.Second

// fillerPrograms schedules a filler clip at t. Trailers of upcoming Discover
// and trailer folder titles get a short slate with their release date, if it
// fits before limit.
func fillerPrograms(f *store.Media, t, limit time.Time) []store.Program {
	stop := t.Add(time.Duration(f.DurationMs) * time.Millisecond)
	if f.Type == store.TypeTrailer && (f.LibraryID == store.OriginTMDb || f.LibraryID == store.OriginInfo) {
		// a TMDb title, the weather or the news is a slate of its own (no video)
		return []store.Program{{Start: t.UnixMilli(), Stop: stop.UnixMilli(), RatingKey: store.PromoKey, Note: f.RatingKey, Kind: store.KindCard, SourceID: "filler"}}
	}
	out := []store.Program{{Start: t.UnixMilli(), Stop: stop.UnixMilli(), RatingKey: f.RatingKey, Kind: store.KindFiller, SourceID: "filler"}}
	if f.Type == store.TypeTrailer && (f.LibraryID == store.OriginDiscover || f.LibraryID == store.OriginFolder) && !stop.Add(promoLen).After(limit) {
		out = append(out, store.Program{Start: stop.UnixMilli(), Stop: stop.Add(promoLen).UnixMilli(),
			RatingKey: store.PromoKey, Note: f.RatingKey, Kind: store.KindCard})
	}
	return out
}

// pickFiller returns a random filler clip no longer than maxDur, avoiding recent repeats.
func (g *generator) pickFiller(t time.Time, maxDur time.Duration) *store.Media {
	return g.pickFillerBetween(t, 0, maxDur)
}

// pickFillerBetween returns a random filler clip lasting minDur to maxDur, avoiding recent repeats.
func (g *generator) pickFillerBetween(t time.Time, minDur, maxDur time.Duration) *store.Media {
	if g.cfg.Filler == nil {
		return nil
	}
	pool := g.fillerPool(t)
	var fits, fresh []*store.Media
	for _, m := range pool {
		if d := time.Duration(m.DurationMs) * time.Millisecond; d > maxDur || d < minDur {
			continue
		}
		fits = append(fits, m)
		if at, ok := g.lastAired[m.RatingKey]; !ok || t.Sub(at) > 3*time.Hour {
			fresh = append(fresh, m)
		}
	}
	if len(fresh) > 0 {
		fits = fresh
	}
	if len(fits) == 0 {
		return nil
	}
	m := fits[g.rng.Intn(len(fits))]
	g.lastAired[m.RatingKey] = t
	return m
}

// ---- persistent per-source state ----

type State struct {
	Sources map[string]*SourceState `json:"sources"`
}

type SourceState struct {
	Last       string            `json:"last,omitempty"`       // fixed order: last aired key
	Resume     string            `json:"resume,omitempty"`     // fixed order: replay this key next (after a re-plan)
	ShowResume map[string]string `json:"showResume,omitempty"` // show key -> episode to replay next (after a re-plan)
	Played     []string          `json:"played,omitempty"`     // shuffle: keys/groups aired this cycle
	LastGroup  string            `json:"lastGroup,omitempty"`  // shuffle with episode order
	ShowCursor map[string]string `json:"showCursor,omitempty"` // show key -> last aired episode key
	Queue      []QueuedPick      `json:"queue,omitempty"`      // AI picks not yet aired
	AIConfig   string            `json:"aiConfig,omitempty"`   // theme and filter the queued AI picks were made for
}

type QueuedPick struct {
	Key    string `json:"key"`
	Reason string `json:"reason,omitempty"`
}

func (st *State) init() {
	if st.Sources == nil {
		st.Sources = map[string]*SourceState{}
	}
}

func (st *State) source(id string) *SourceState {
	ss, ok := st.Sources[id]
	if !ok {
		ss = &SourceState{}
		st.Sources[id] = ss
	}
	if ss.ShowCursor == nil {
		ss.ShowCursor = map[string]string{}
	}
	return ss
}

// ---- generator: picks the next item for a point in time ----

type generator struct {
	s         *Scheduler
	ctx       context.Context
	ch        *store.Channel
	cfg       *Config
	state     *State
	catalog   []*store.Media
	rng       *rand.Rand
	byKey     map[string]*store.Media
	episodes  map[string][]*store.Media // show key -> episodes in order
	resolved  map[string][]*store.Media
	lastAired map[string]time.Time
	softErr   error
	// Failed requests pause this source for this planning run only. The next run
	// retries; at most one random pick per run may extend the guide, and only
	// while it covers less than half the horizon.
	aiFailures      map[string]error
	aiFallbackUntil time.Time
	aiFallbackUsed  bool
	langs           []string         // preferred trailer languages
	trailers        []*store.Trailer // loaded on first use
	info            []*store.Media   // weather and news slates, loaded on first use
	// overrun: the last clip of a break may end this much after the round start
	// time (0 where the following times are fixed, see Substitute).
	overrun time.Duration
}

func (g *generator) index() {
	g.byKey = make(map[string]*store.Media, len(g.catalog))
	g.episodes = map[string][]*store.Media{}
	for _, m := range g.catalog {
		g.byKey[m.RatingKey] = m
		if m.Type == "episode" {
			g.episodes[m.ShowKey] = append(g.episodes[m.ShowKey], m)
		}
	}
	for _, eps := range g.episodes {
		sortEpisodes(eps)
	}
}

func sortEpisodes(eps []*store.Media) {
	sort.SliceStable(eps, func(i, j int) bool {
		if eps[i].Season != eps[j].Season {
			// specials (season 0) go last
			if eps[i].Season == 0 || eps[j].Season == 0 {
				return eps[j].Season == 0
			}
			return eps[i].Season < eps[j].Season
		}
		return eps[i].Episode < eps[j].Episode
	})
}

func (g *generator) next(t time.Time) (*store.Media, string, string, string, error) {
	type candidate struct {
		id   string
		src  *Source
		name string
	}
	var order []candidate
	if g.ch.Kind == KindSeasonal {
		local := t.Local()
		if season := g.cfg.SeasonAt(local); season != nil {
			order = append(order, candidate{"season:" + season.ID, &season.Source, strings.TrimSpace(season.Emoji + " " + season.Name)})
		}
		if g.cfg.Fallback != nil {
			order = append(order, candidate{"fallback", g.cfg.Fallback, ""})
		}
	} else {
		order = append(order, candidate{"main", &g.cfg.Source, ""})
	}
	for _, c := range order {
		item, note, err := g.pick(c.id, c.src, t)
		if errors.Is(err, ErrNoContent) {
			continue
		}
		if err != nil {
			return nil, "", "", "", err
		}
		return item, c.id, c.name, note, nil
	}
	return nil, "", "", "", ErrNoContent
}

func (g *generator) pick(id string, src *Source, t time.Time) (*store.Media, string, error) {
	ss := g.state.source(id)
	if src.Type == "ai" {
		return g.pickAI(id, ss, src, t)
	}
	items := g.resolve(id, src)
	if len(items) == 0 {
		return nil, "", ErrNoContent
	}
	if g.cfg.Order == "fixed" {
		return g.pickFixed(ss, items), "", nil
	}
	if g.cfg.KeepEpisodeOrder {
		return g.pickGrouped(ss, items, t), "", nil
	}
	return g.pickShuffle(ss, items, t), "", nil
}

// resolve expands a source into its ordered list of playable items (cached per run).
func (g *generator) resolve(id string, src *Source) []*store.Media {
	if items, ok := g.resolved[id]; ok {
		return items
	}
	var items []*store.Media
	switch src.Type {
	case "manual":
		for _, it := range src.Items {
			switch it.Type {
			case "show":
				items = append(items, g.episodes[it.Key]...)
			case "season":
				for _, e := range g.episodes[it.Key] {
					if e.Season == it.Season {
						items = append(items, e)
					}
				}
			default:
				if m := g.byKey[it.Key]; m != nil {
					items = append(items, m)
				}
			}
		}
	case "rules":
		items = src.Rules.Filter(g.catalog)
		sortItems(items, g.cfg.SortBy)
	case "trailers":
		items = g.trailerItems(src, nil)
	}
	g.resolved[id] = items
	return items
}

// fillerPool returns the filler clips for time t. Trailers following the
// channel's filter depend on the season airing at t, so they are cached per source.
func (g *generator) fillerPool(t time.Time) []*store.Media {
	src := g.cfg.Filler
	if src.Type != "trailers" || !src.ChannelFilter {
		return g.withInfo(g.resolve("filler", src))
	}
	extra, id := g.channelFilter(t)
	id = "filler@" + id
	if items, ok := g.resolved[id]; ok {
		return g.withInfo(items)
	}
	items := g.trailerItems(src, extra)
	g.resolved[id] = items
	return g.withInfo(items)
}

// ShowsSlates reports whether a channel's breaks show the slates standing for
// "cinema & streaming" (TMDb, weather and news on Jellyfin and Emby).
func ShowsSlates(cfg *Config) bool {
	f := cfg.Filler
	return f != nil && f.Type == "trailers" && (f.TrailerOrigin == "all" || f.TrailerOrigin == store.OriginDiscover)
}

// infoSlates are the weather and news slates of Jellyfin and Emby
// installations: with trailers they fill the breaks that also show the
// "coming to theaters" slates, so those do not repeat all the time.
var infoSlates = []struct{ key, setting string }{
	{"weather-now", store.SetWeatherPlace}, {"weather-days", store.SetWeatherPlace},
	{"news-1", store.SetNewsFeeds}, {"news-2", store.SetNewsFeeds},
}

// withInfo adds the weather and news slates to a filler pool.
func (g *generator) withInfo(pool []*store.Media) []*store.Media {
	if !ShowsSlates(g.cfg) {
		return pool
	}
	if g.info == nil && g.s != nil && g.s.store != nil {
		g.info = []*store.Media{}
		st := g.s.store
		if p := st.Setting(g.ctx, store.SetMediaServer, "plex"); p == "jellyfin" || p == "emby" {
			sec := min(max(st.SettingInt(g.ctx, store.SetSlateSeconds, 20), 8), 60)
			for _, in := range infoSlates {
				if st.Setting(g.ctx, in.setting, "") != "" {
					g.info = append(g.info, &store.Media{RatingKey: store.InfoPrefix + in.key, Type: store.TypeTrailer,
						LibraryID: store.OriginInfo, Title: in.key, DurationMs: int64(sec) * 1000})
				}
			}
		}
	}
	if len(g.info) == 0 {
		return pool
	}
	return slices.Concat(pool, g.info)
}

// channelFilter returns the filter of the content source airing at t and its id
// (nil for manual selections, which have no filter to share).
func (g *generator) channelFilter(t time.Time) (*rules.Group, string) {
	id, src := "main", &g.cfg.Source
	if g.ch != nil && g.ch.Kind == KindSeasonal {
		id, src = "fallback", g.cfg.Fallback
		if season := g.cfg.SeasonAt(t.Local()); season != nil {
			id, src = "season:"+season.ID, &season.Source
		}
	}
	switch {
	case src == nil:
		return nil, id
	case src.Type == "rules":
		return src.Rules, id
	case src.Type == "ai" && src.AI != nil:
		return src.AI.Filter, id
	}
	return nil, id
}

// discoverUnknown are rule fields Plex Discover titles carry no (comparable)
// value for; rules on them are skipped for those trailers. Duration would be
// the trailer's, not the film's.
var discoverUnknown = map[string]bool{
	"library": true, "watched": true, "addedDays": true, "actor": true, "director": true, "writer": true,
	"collection": true, "label": true, "show": true, "season": true, "duration": true, "studio": true,
	"country": true, "rating": true,
}

// trailerItems returns one trailer per advertised title in the best preferred
// language, for titles matching the source's filter and, if given, extra.
func (g *generator) trailerItems(src *Source, extra *rules.Group) []*store.Media {
	if g.trailers == nil {
		all, err := g.s.store.Trailers(g.ctx)
		if err != nil {
			g.s.log.Warn("load trailers", "err", err)
		}
		g.trailers = all
	}
	now := time.Now()
	own, ownDiscover := src.Rules, src.Rules.Without(discoverUnknown)
	extraDiscover := extra.Without(discoverUnknown)
	best := map[string]*store.Trailer{}
	rank := map[string]int{}
	var order []string
	for _, t := range g.trailers {
		// TMDb slates and the trailer folder stand for "cinema & streaming"
		// (where Plex Discover is missing, or besides it)
		origin := t.Origin
		if origin == store.OriginTMDb || origin == store.OriginFolder {
			origin = store.OriginDiscover
		}
		if src.TrailerOrigin != "all" && src.TrailerOrigin != origin {
			continue
		}
		// slates have no audio track; they only stand in where no video of the
		// trailer folder advertises the title (same guid)
		r := 1 << 20
		if t.Origin != store.OriginTMDb {
			if r = store.LanguageRank(t.Language, g.langs); r < 0 {
				continue
			}
		}
		item, ownRules, extraRules := t.Item(), ownDiscover, extraDiscover
		if t.Origin == store.OriginLibrary {
			item = g.byKey[t.MediaKey]
			if eps := g.episodes[t.ShowKey]; t.ShowKey != "" && len(eps) > 0 {
				item = eps[0] // a show matches the rules like its episodes
			}
			if item == nil {
				continue // library disabled or title deleted
			}
			ownRules, extraRules = own, extra
		}
		if !ownRules.Matches(item, now) || !extraRules.Matches(item, now) {
			continue
		}
		id := t.Advertised()
		if id == "" {
			id = t.GUID
		}
		if cur, ok := best[id]; ok && rank[cur.Key] <= r {
			continue
		} else if !ok {
			order = append(order, id)
		}
		best[id], rank[t.Key] = t, r
	}
	out := make([]*store.Media, 0, len(order))
	for _, id := range order {
		out = append(out, best[id].Media(720))
	}
	return out
}

func sortItems(items []*store.Media, by string) {
	less := func(a, b *store.Media) bool {
		if a.SortTitle != b.SortTitle {
			return a.SortTitle < b.SortTitle
		}
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		return a.Episode < b.Episode
	}
	switch by {
	case "year", "release":
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i], items[j]
			ka, kb := a.Released, b.Released
			if by == "year" || ka == "" || kb == "" {
				ka, kb = fmt.Sprint(a.Year), fmt.Sprint(b.Year)
			}
			if ka != kb {
				return ka < kb
			}
			return less(a, b)
		})
	case "added":
		sort.SliceStable(items, func(i, j int) bool { return items[i].AddedAt > items[j].AddedAt })
	default:
		sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
	}
}

func (g *generator) pickFixed(ss *SourceState, items []*store.Media) *store.Media {
	if ss.Resume != "" {
		resume := ss.Resume
		ss.Resume = ""
		for _, m := range items {
			if m.RatingKey == resume {
				ss.Last = m.RatingKey
				return m
			}
		}
		// The item is gone (deleted from Plex): continue after the last kept one.
	}
	next := 0
	for i, m := range items {
		if m.RatingKey == ss.Last {
			next = (i + 1) % len(items)
			break
		}
	}
	ss.Last = items[next].RatingKey
	return items[next]
}

func (g *generator) recent(key string, t time.Time) bool {
	if g.cfg.NoRepeatHours <= 0 {
		return false
	}
	at, ok := g.lastAired[key]
	return ok && t.Sub(at) < time.Duration(g.cfg.NoRepeatHours)*time.Hour
}

// pickShuffle plays every item once in random order before starting a new cycle.
func (g *generator) pickShuffle(ss *SourceState, items []*store.Media, t time.Time) *store.Media {
	played := make(map[string]bool, len(ss.Played))
	for _, k := range ss.Played {
		played[k] = true
	}
	pool := filter(items, func(m *store.Media) bool { return !played[m.RatingKey] && !g.recent(m.RatingKey, t) })
	if len(pool) == 0 {
		ss.Played = nil
		pool = filter(items, func(m *store.Media) bool { return !g.recent(m.RatingKey, t) && m.RatingKey != ss.Last })
	}
	if len(pool) == 0 {
		pool = items
	}
	m := pool[g.rng.Intn(len(pool))]
	ss.Played = append(ss.Played, m.RatingKey)
	ss.Last = m.RatingKey
	return m
}

// pickGrouped shuffles between movies and series, but series continue with their next episode.
func (g *generator) pickGrouped(ss *SourceState, items []*store.Media, t time.Time) *store.Media {
	groups := map[string][]*store.Media{}
	var keys []string
	for _, m := range items {
		k := m.RatingKey
		if m.Type == "episode" {
			k = "show:" + m.ShowKey
		}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], m)
	}
	played := map[string]bool{}
	for _, k := range ss.Played {
		played[k] = true
	}
	eligible := func(k string, strict bool) bool {
		if len(keys) > 1 && k == ss.LastGroup {
			return false
		}
		if strict && played[k] {
			return false
		}
		if !strings.HasPrefix(k, "show:") && g.recent(k, t) {
			return false
		}
		return true
	}
	var pool []string
	for _, k := range keys {
		if eligible(k, true) {
			pool = append(pool, k)
		}
	}
	if len(pool) == 0 {
		ss.Played = nil
		for _, k := range keys {
			if eligible(k, false) {
				pool = append(pool, k)
			}
		}
	}
	if len(pool) == 0 {
		pool = keys
	}
	k := pool[g.rng.Intn(len(pool))]
	ss.Played = append(ss.Played, k)
	ss.LastGroup = k
	members := groups[k]
	if !strings.HasPrefix(k, "show:") {
		return members[0]
	}
	return g.nextEpisode(ss, strings.TrimPrefix(k, "show:"), members)
}

// nextEpisode continues a series after the last aired episode (wrapping at the end).
func (g *generator) nextEpisode(ss *SourceState, show string, eps []*store.Media) *store.Media {
	if resume, ok := ss.ShowResume[show]; ok {
		delete(ss.ShowResume, show)
		for _, e := range eps {
			if e.RatingKey == resume {
				ss.ShowCursor[show] = e.RatingKey
				return e
			}
		}
	}
	last := ss.ShowCursor[show]
	next := 0
	for i, e := range eps {
		if e.RatingKey == last {
			next = (i + 1) % len(eps)
			break
		}
	}
	ss.ShowCursor[show] = eps[next].RatingKey
	return eps[next]
}

func filter(items []*store.Media, keep func(*store.Media) bool) []*store.Media {
	var out []*store.Media
	for _, m := range items {
		if keep(m) {
			out = append(out, m)
		}
	}
	return out
}

// ---- AI curated sources ----

// DefaultAICandidates is how many titles one AI request offers at most when
// the setting is not set: about 60,000 tokens.
const DefaultAICandidates = 800

// MinAICandidates and MaxAICandidates bound the setting.
const (
	MinAICandidates = 50
	MaxAICandidates = 5000
)

func (g *generator) pickAI(id string, ss *SourceState, src *Source, t time.Time) (*store.Media, string, error) {
	cfg := src.AI
	if cfg == nil {
		return nil, "", ErrNoContent
	}
	// Picks made for another theme or filter are dropped, and the AI is asked right
	// away, so an edited prompt shows in the guide at once.
	if fp := aiFingerprint(cfg); ss.AIConfig != fp {
		ss.Queue, ss.AIConfig = nil, fp
		delete(g.aiFailures, id)
	}
	for len(ss.Queue) > 0 {
		q := ss.Queue[0]
		ss.Queue = ss.Queue[1:]
		if m := g.byKey[q.Key]; m != nil {
			return m, q.Reason, nil
		}
	}

	pool := g.catalog
	if !cfg.Filter.IsEmpty() {
		pool = cfg.Filter.Filter(g.catalog)
	}
	if len(pool) == 0 {
		return nil, "", ErrNoContent
	}
	if g.aiFailures[id] == nil {
		if err := g.refillAI(ss, cfg, pool, t); err != nil {
			g.s.log.Warn("ai selection failed, allowing at most one random pick below half the guide horizon", "channel", g.ch.Name, "err", err)
			if g.aiFailures == nil {
				g.aiFailures = map[string]error{}
			}
			g.softErr = i18n.E("schedule.aiFailed", err)
			g.aiFailures[id] = g.softErr
		} else if len(ss.Queue) > 0 {
			q := ss.Queue[0]
			ss.Queue = ss.Queue[1:]
			if m := g.byKey[q.Key]; m != nil {
				return m, q.Reason, nil
			}
		}
	}
	// Leave the remaining guide unfilled while enough programme is prepared.
	// Returning the failure stops this run without discarding its progress.
	if g.aiFallbackUsed || !t.Before(g.aiFallbackUntil) {
		return nil, "", g.aiFailures[id]
	}
	// Keep the guide mostly open for the next AI retry. The allowance belongs
	// to the whole channel's planning run, including channels with several sources.
	g.aiFallbackUsed = true
	if g.cfg.KeepEpisodeOrder {
		return g.pickGrouped(ss, pool, t), "", nil
	}
	return g.pickShuffle(ss, pool, t), "", nil
}

// aiFingerprint identifies what AI picks depend on: theme, pre-selection and
// episodes per show.
func aiFingerprint(cfg *AIConfig) string {
	b, _ := json.Marshal(struct {
		Theme    string
		Filter   any
		Episodes int
	}{strings.TrimSpace(cfg.Theme), cfg.Filter, cfg.EpisodesPerShow})
	return string(b)
}

func (g *generator) refillAI(ss *SourceState, cfg *AIConfig, pool []*store.Media, t time.Time) error {
	provider, err := g.s.ai(g.ctx)
	if err != nil {
		return err
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 12
	}
	perShow := cfg.EpisodesPerShow
	if perShow <= 0 {
		perShow = 2
	}

	// Movies are offered individually, series as a whole. score counts the words
	// of the theme found in each title's metadata (full summary, not the excerpt).
	var cands []ai.Candidate
	words := themeWords(cfg.Theme)
	score := map[string]int{}
	seenShow := map[string]bool{}
	window := time.Duration(max(g.cfg.NoRepeatHours, 24)) * time.Hour
	for _, m := range pool {
		if m.Type == "episode" {
			if seenShow[m.ShowKey] {
				continue
			}
			seenShow[m.ShowKey] = true
			cands = append(cands, ai.Candidate{ID: "show:" + m.ShowKey, Kind: "show", Title: m.ShowTitle, Year: m.Year,
				Genres: m.Tags.Genre, Collections: m.Tags.Collection, Summary: truncate(m.ShowSummary, 140)})
			score["show:"+m.ShowKey] = matchCount(words, m.ShowTitle, strings.Join(m.Tags.Collection, " "), strings.Join(m.Tags.Genre, " "), m.ShowSummary)
			continue
		}
		if at, ok := g.lastAired[m.RatingKey]; ok && t.Sub(at) < window {
			continue
		}
		cands = append(cands, ai.Candidate{ID: m.RatingKey, Kind: "movie", Title: m.Title, Year: m.Year,
			Genres: m.Tags.Genre, Collections: m.Tags.Collection, Summary: truncate(m.Summary, 140)})
		score[m.RatingKey] = matchCount(words, m.Title, strings.Join(m.Tags.Collection, " "), strings.Join(m.Tags.Genre, " "), m.Summary)
	}
	if len(cands) == 0 {
		return ErrNoContent
	}
	if limit := g.s.aiCandidates(g.ctx); len(cands) > limit {
		// Too many for one request: titles mentioning words of the theme first
		// (a random sample could miss a niche theme entirely), the rest at random.
		g.rng.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
		sort.SliceStable(cands, func(i, j int) bool { return score[cands[i].ID] > score[cands[j].ID] })
		cands = cands[:limit]
	}

	var recent []string
	for key, at := range g.lastAired {
		if t.Sub(at) < 7*24*time.Hour {
			if m := g.byKey[key]; m != nil {
				recent = append(recent, m.DisplayTitle())
			}
		}
	}
	sort.Strings(recent)
	recent = dedupe(recent)
	if len(recent) > 60 {
		recent = recent[len(recent)-60:]
	}

	ctx, cancel := context.WithTimeout(g.ctx, ai.RequestTimeout)
	defer cancel()
	lang := g.s.lang(ctx)
	date := t.Local().Format("Monday, January 2, 2006, 3:04 PM")
	if lang == i18n.DE {
		date = t.Local().Format("Monday, 2. January 2006, 15:04 Uhr")
	}
	started := time.Now()
	picks, err := provider.Pick(ctx, ai.Request{
		Theme: cfg.Theme, Count: batch, Candidates: cands, Recent: recent, Date: date, Lang: lang,
	})
	if err != nil {
		return err
	}
	g.s.log.Debug("ai selection", "channel", g.ch.Name, "candidates", len(cands), "picks", len(picks),
		"took", time.Since(started).Round(100*time.Millisecond))
	queued := len(ss.Queue)
	for _, p := range picks {
		if strings.HasPrefix(p.ID, "show:") {
			show := strings.TrimPrefix(p.ID, "show:")
			eps := g.episodes[show]
			if len(eps) == 0 {
				continue
			}
			for i := 0; i < perShow; i++ {
				e := g.nextEpisode(ss, show, eps)
				ss.Queue = append(ss.Queue, QueuedPick{Key: e.RatingKey, Reason: p.Reason})
			}
			continue
		}
		ss.Queue = append(ss.Queue, QueuedPick{Key: p.ID, Reason: p.Reason})
	}
	if len(ss.Queue) == queued {
		return i18n.E("ai.empty")
	}
	return nil
}

// themeStopwords are frequent words of themes that say nothing about a title.
var themeStopwords = map[string]bool{
	"filme": true, "film": true, "serie": true, "serien": true, "folgen": true, "titel": true, "einen": true,
	"eine": true, "einer": true, "nicht": true, "aber": true, "sind": true, "oder": true, "auch": true,
	"alle": true, "sehr": true, "dass": true, "haben": true, "werden": true, "etwas": true, "ohne": true,
	"beim": true, "über": true, "mit": true, "und": true, "die": true, "der": true, "das": true, "für": true,
	"von": true, "aus": true, "zum": true, "zur": true, "wie": true, "gerne": true, "bitte": true, "schöne": true,
}

// themeWords splits a theme into words worth searching for (4+ letters).
func themeWords(theme string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(theme), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) >= 4 && !themeStopwords[w] && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// matchCount counts the theme words found in any of the texts.
func matchCount(words []string, texts ...string) int {
	all := strings.ToLower(strings.Join(texts, " "))
	n := 0
	for _, w := range words {
		if strings.Contains(all, w) {
			n++
		}
	}
	return n
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// Preview returns the items a source definition currently matches (for the editor).
// kind and cfg describe the channel being edited (optional); trailer sources
// following the channel's filter need them.
func (s *Scheduler) Preview(ctx context.Context, src *Source, sortBy, kind string, cfg *Config) ([]*store.Media, error) {
	catalog, err := s.lib.All(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &Config{}
	}
	cfg.SortBy = sortBy
	g := &generator{s: s, ctx: ctx, ch: &store.Channel{Kind: kind}, cfg: cfg, catalog: catalog,
		resolved: map[string][]*store.Media{}, langs: s.TrailerLanguages(ctx)}
	g.index()
	if src.Type == "trailers" && src.ChannelFilter {
		extra, _ := g.channelFilter(time.Now())
		return g.trailerItems(src, extra), nil
	}
	if src.Type == "ai" {
		if src.AI == nil || src.AI.Filter.IsEmpty() {
			return catalog, nil
		}
		return src.AI.Filter.Filter(catalog), nil
	}
	return g.resolve("preview", src), nil
}
