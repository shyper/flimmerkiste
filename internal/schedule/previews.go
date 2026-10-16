package schedule

import (
	"context"
	"errors"
	"math/rand"
	"strconv"
	"time"

	"github.com/shyper/flimmerkiste/internal/store"
)

const (
	// bindAhead: preview slots are bound this long before they air. The guide
	// (horizon) is known far beyond, so every slot finds its candidates.
	bindAhead = 2 * time.Hour
	// Advertised titles start at least previewGapMin after the slot (the next
	// title has its own slate) and at most previewReach later.
	previewGapMin = 30 * time.Minute
	previewReach  = 24 * time.Hour
)

// BindPreviews fills the preview slots of a channel that start before until.
func (s *Scheduler) BindPreviews(ctx context.Context, channelID int64, until time.Time) error {
	l := s.lock(channelID)
	l.Lock()
	defer l.Unlock()
	return s.bindPreviews(ctx, channelID, until)
}

// BindPreviewsNow is BindPreviews for a running stream: ErrBusy instead of
// waiting while the channel is being planned (see EnsureNow).
func (s *Scheduler) BindPreviewsNow(ctx context.Context, channelID int64, until time.Time) error {
	l := s.lock(channelID)
	if !tryLock(ctx, l) {
		return ErrBusy
	}
	defer l.Unlock()
	return s.bindPreviews(ctx, channelID, until)
}

func (s *Scheduler) bindPreviews(ctx context.Context, channelID int64, until time.Time) error {
	now := time.Now()
	progs, err := s.store.ProgramsBetween(ctx, channelID, now.Add(-12*time.Hour), until.Add(previewReach))
	if err != nil {
		return err
	}
	var slots []store.Program
	promoted := map[int64]bool{}        // program starts already advertised
	lastPromo := map[string]time.Time{} // title -> last time its trailer ran
	for _, p := range progs {
		switch {
		case p.Kind == store.KindPreview && p.RatingKey == store.PreviewKey && p.Start < until.UnixMilli():
			slots = append(slots, p)
		case p.Kind == store.KindCard && p.RatingKey == store.PromoKey:
			if ms, err := strconv.ParseInt(p.Note, 10, 64); err == nil {
				promoted[ms] = true
			}
		}
	}
	if len(slots) == 0 {
		return nil
	}
	trailers, err := s.previewTrailers(ctx)
	if err != nil {
		return err
	}
	for _, p := range progs {
		if p.Kind == store.KindFiller && trailers.byKey[p.RatingKey] != nil {
			lastPromo[trailers.byKey[p.RatingKey].MediaKey] = p.StartTime()
		}
	}
	rng := rand.New(rand.NewSource(now.UnixNano()))
	for _, slot := range slots {
		repl := s.bindSlot(slot, progs, trailers, promoted, lastPromo, rng)
		if err := s.store.ReplaceProgram(ctx, slot.ID, repl); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

type trailerIndex struct {
	byMedia map[string]*store.Trailer // library movie -> best trailer
	byKey   map[string]*store.Trailer
}

// previewTrailers returns the best library trailer per movie in a preferred language.
func (s *Scheduler) previewTrailers(ctx context.Context) (*trailerIndex, error) {
	all, err := s.store.Trailers(ctx)
	if err != nil {
		return nil, err
	}
	langs := s.TrailerLanguages(ctx)
	idx := &trailerIndex{byMedia: map[string]*store.Trailer{}, byKey: map[string]*store.Trailer{}}
	rank := map[string]int{}
	for _, t := range all {
		r := store.LanguageRank(t.Language, langs)
		if t.Origin != store.OriginLibrary || r < 0 {
			continue
		}
		idx.byKey[t.Key] = t
		if cur, ok := idx.byMedia[t.MediaKey]; !ok || r < rank[cur.Key] {
			idx.byMedia[t.MediaKey], rank[t.Key] = t, r
		}
	}
	return idx, nil
}

// bindSlot picks a later title of the channel whose trailer fits the slot and
// returns the programs replacing it: trailer, promo slate, "up next" slate.
// Without a candidate the slot becomes a plain slate.
func (s *Scheduler) bindSlot(slot store.Program, progs []store.Program, trailers *trailerIndex,
	promoted map[int64]bool, lastPromo map[string]time.Time, rng *rand.Rand) []store.Program {
	length := slot.StopTime().Sub(slot.StartTime())
	from := slot.StopTime().Add(previewGapMin).UnixMilli()
	to := slot.StartTime().Add(previewReach).UnixMilli()
	var best *store.Program
	var bestTrailer *store.Trailer
	bestScore := -1 << 30
	for i := range progs {
		p := &progs[i]
		if !p.IsMain() || p.Start < from || p.Start > to || promoted[p.Start] {
			continue
		}
		t := trailers.byMedia[p.RatingKey]
		if t == nil || time.Duration(t.DurationMs)*time.Millisecond+promoLen+time.Second > length {
			continue
		}
		score := rng.Intn(3)
		if h := p.StartTime().Local().Hour(); h >= 19 && h <= 22 {
			score += 4 // prime time is worth announcing
		}
		if at, ok := lastPromo[p.RatingKey]; ok && slot.StartTime().Sub(at) < 6*time.Hour {
			score -= 10 // shown recently
		}
		if score > bestScore {
			best, bestTrailer, bestScore = p, t, score
		}
	}
	base := store.Program{ChannelID: slot.ChannelID, SourceID: slot.SourceID}
	if best == nil {
		p := base
		p.Start, p.Stop, p.RatingKey, p.Kind = slot.Start, slot.Stop, store.CardKey, store.KindCard
		return []store.Program{p}
	}
	promoted[best.Start] = true
	lastPromo[best.RatingKey] = slot.StartTime()

	trailerStop := slot.Start + bestTrailer.DurationMs
	promoStop := trailerStop + promoLen.Milliseconds()
	if slot.Stop-promoStop < minCard.Milliseconds() {
		promoStop = slot.Stop // too short for its own slate
	}
	out := []store.Program{base, base}
	out[0].Start, out[0].Stop, out[0].RatingKey, out[0].Kind = slot.Start, trailerStop, bestTrailer.Key, store.KindFiller
	out[1].Start, out[1].Stop, out[1].RatingKey, out[1].Kind = trailerStop, promoStop, store.PromoKey, store.KindCard
	out[1].Note = strconv.FormatInt(best.Start, 10)
	if promoStop < slot.Stop {
		rest := base
		rest.Start, rest.Stop, rest.RatingKey, rest.Kind = promoStop, slot.Stop, store.CardKey, store.KindCard
		out = append(out, rest)
	}
	return out
}

// BindAllPreviews binds the slots of all channels airing within bindAhead.
func (s *Scheduler) BindAllPreviews(ctx context.Context) {
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		return
	}
	until := time.Now().Add(bindAhead)
	for _, ch := range chans {
		if !ch.Enabled {
			continue
		}
		if err := s.BindPreviews(ctx, ch.ID, until); err != nil {
			s.log.Warn("bind previews", "channel", ch.Name, "err", err)
		}
	}
}

// Substitute fills [from, to) on a channel whose title there (failed) cannot be
// played: with the channel's filler clips – or, without any, library trailers
// matching the channel – and the "up next" slate for the rest. Without clips
// the fault slate covers it all. The guide stays as it is, so the following
// titles start on time.
func (s *Scheduler) Substitute(ctx context.Context, ch *store.Channel, failed string, from, to time.Time) []store.Program {
	slate := []store.Program{{ChannelID: ch.ID, Start: from.UnixMilli(), Stop: to.UnixMilli(), RatingKey: store.FaultKey, Note: failed, Kind: store.KindCard}}
	cfg, err := ParseConfig(ch.Config)
	if err != nil {
		return slate
	}
	catalog, err := s.lib.All(ctx)
	if err != nil {
		return slate
	}
	cfg.Previews = false
	if cfg.Filler == nil {
		cfg.Filler = &Source{Type: "trailers", TrailerOrigin: "library", ChannelFilter: true}
		cfg.Filler.normalize()
	}
	g := &generator{s: s, ctx: ctx, ch: ch, cfg: cfg, catalog: catalog,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
		resolved: map[string][]*store.Media{}, lastAired: map[string]time.Time{}, langs: s.TrailerLanguages(ctx)}
	g.index()
	progs := g.fillGap(from, to)
	if len(progs) <= 1 {
		return slate // no clips: say what happened instead of a plain "up next"
	}
	for i := range progs {
		progs[i].ChannelID = ch.ID
	}
	return progs
}
