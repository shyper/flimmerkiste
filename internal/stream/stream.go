// Package stream produces the continuous MPEG-TS stream Plex receives from
// the virtual tuner. ffmpeg only runs while someone is watching; it seeks
// straight to the scheduled position and plays item after item in real time.
package stream

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
	"github.com/shyper/flimmerkiste/internal/store"
)

type ServerFunc func(ctx context.Context) (mediaserver.Server, error)

// ProgramFunc returns the program airing on a channel at t (generating the guide if needed).
type ProgramFunc func(ctx context.Context, channelID int64, t time.Time) (*store.Program, error)

type Options struct {
	Mode           string // copy | audio | full
	NormalizeRes   int    // 720 / 1080 for full mode
	AudioLanguages []string
	Watermark      *Watermark    // burned-in channel logo (full mode only)
	Caption        string        // lower third PNG for the current trailer (full mode only, set per item)
	Subtitles      *Subtitles    // forced subtitles to burn in (full mode only, set per item)
	ToneMap        string        // filters turning an HDR picture into SDR, "," first ("" = none; full mode only, set per item)
	Buffer         time.Duration // content the client is kept ahead by (default 10s)
	VAAPI          string        // render node for GPU encoding in full mode ("" = software)
	GPUEncoder     GPUEncoder    // encoder settings the GPU accepted (see VAAPI.Encoder)
	GPUDecode      bool          // the GPU also decodes (only with VAAPI; see VAAPI.Decode)
	Aspect         float64       // display aspect ratio of the source (0 = unknown: fills the frame; set per item)
	LogoFadeIn     bool          // the logo fades in (it was not visible before; set per item)
	LogoFadeOut    bool          // the logo fades out at the end (a slate or another group follows, see logoGroup; set per item)
	// LogoFrom is where the logo was at the end of the previous item (empty: not
	// shown); if it lands elsewhere now, it moves by fading (set per item).
	LogoFrom image.Rectangle
	Credits  bool // shrink the end credits next to an info panel (full mode only)
	// CardOverlay: the small card over credits that are not shrunk (full mode
	// only, set per item; a trailer's caption takes precedence).
	CardOverlay *CardOverlay

	// part is the file of a title stored in several files that play streams;
	// parted marks such a call (see playParts).
	part   int
	parted bool
}

// DefaultBuffer is how far the stream runs ahead of real time, so slow starts
// of the next item (remote trailers take 1–3s) never reach the screen.
const DefaultBuffer = 10 * time.Second

type Watermark struct {
	Path     string // PNG file
	Position string // tl | tr | bl | br
	Opacity  int    // percent
	Size     int    // logo width in percent of the picture width
}

// CaptionFunc returns the lower third PNG for a trailer program ("" = none).
type CaptionFunc func(ctx context.Context, ch *store.Channel, prog *store.Program, height int) (string, error)

// CardFunc renders the "up next" slate for a card program and returns the PNG path.
type CardFunc func(ctx context.Context, ch *store.Channel, prog *store.Program, height int) (string, error)

// CardAudioFunc returns the audio for the slate of prog: the file ("" = silence)
// and whether it is the theme music of a title, which plays once instead of
// looping.
type CardAudioFunc func(ctx context.Context, ch *store.Channel, prog *store.Program) (path string, theme bool)

// themeFade: theme music fades out over the end of the slate.
const themeFade = 2 * time.Second

type Session struct {
	ID        int64     `json:"id"`
	ChannelID int64     `json:"channelId"`
	Channel   string    `json:"channel"`
	Client    string    `json:"client"`
	Started   time.Time `json:"started"`
	Title     string    `json:"title"`
}

type Streamer struct {
	FFmpeg    string
	Server    ServerFunc
	Program   ProgramFunc
	Media     func(ctx context.Context, key string) (*store.Media, error)
	Card      CardFunc
	CardAudio CardAudioFunc
	// CardPages renders the pages of long "up next" slates (optional, see pages.go).
	CardPages CardPagesFunc
	Caption   CaptionFunc
	Credits   CreditsFunc
	// CreditsCard renders the small card over credits that are not shrunk (optional).
	CreditsCard CreditsCardFunc
	Clock       ClockFunc // time at the top right of the credits panel (optional)
	// ClockText writes a time of day for Clock (nil = 24 hours, "20:15").
	ClockText func(time.Time) string
	// LocalFile returns where a file Plex knows by its path can be read
	// directly ("" = stream it from Plex).
	LocalFile func(ctx context.Context, plexPath string) string
	Log       *slog.Logger
	// Prefetch returns the URL of a short remote clip worth downloading before it
	// airs ("" for everything else). Files go to CacheDir and are removed after use.
	Prefetch func(ctx context.Context, prog *store.Program) string
	CacheDir string
	// Substitute returns what fills [from, to) when a title cannot be played
	// (clips, trailers, slate), so the following titles stay on time.
	Substitute func(ctx context.Context, ch *store.Channel, failed string, from, to time.Time) []store.Program

	nextID   atomic.Int64
	mu       sync.Mutex
	sessions map[int64]*Session
	lastEnd  time.Time // when the last session ended

	filtersOnce      sync.Once // the ffmpeg filters for tone mapping, looked up once
	tonemapx, zscale bool
}

// Activity returns the number of running streams and when the last one ended.
func (s *Streamer) Activity() (int, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions), s.lastEnd
}

func (s *Streamer) Sessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		out = append(out, *ss)
	}
	return out
}

func (s *Streamer) track(sess *Session) func() {
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[int64]*Session{}
	}
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.sessions, sess.ID)
		s.lastEnd = time.Now()
		s.mu.Unlock()
	}
}

func (s *Streamer) setTitle(id int64, title string) {
	s.mu.Lock()
	if ss, ok := s.sessions[id]; ok {
		ss.Title = title
	}
	s.mu.Unlock()
}

// Serve streams a channel until the client disconnects.
func (s *Streamer) Serve(w http.ResponseWriter, r *http.Request, ch *store.Channel, opts Options) {
	ctx := r.Context()
	sess := &Session{ID: s.nextID.Add(1), ChannelID: ch.ID, Channel: ch.Name, Client: r.RemoteAddr, Started: time.Now()}
	defer s.track(sess)()
	log := s.Log.With("channel", ch.Name, "session", sess.ID)
	log.Info("stream started", "client", r.RemoteAddr)
	defer log.Info("stream stopped")

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	out := &flushWriter{w: w, rc: http.NewResponseController(w)}

	target := opts.Buffer
	if target <= 0 {
		target = DefaultBuffer
	}
	pf := &prefetcher{dir: s.CacheDir, session: sess.ID, log: log}
	defer pf.close()

	// cursor is the schedule position we are streaming; it advances by exactly the
	// amount of content sent, so drift relative to the guide stays minimal.
	// sent is the content duration delivered so far. Output timestamps continue
	// from it without gaps, so a slow start of the next item only drains the
	// client's buffer instead of freezing the picture. Every item starts with a
	// burst that tops the buffer up to target again.
	began := time.Now()
	cursor := began
	var sent time.Duration
	// topUp is the burst that brings the client's buffer back to target once
	// sent has been delivered.
	topUp := func(sent time.Duration) time.Duration {
		return min(max(target-(sent-time.Since(began)), 0), 3*target)
	}
	failures, retries := 0, 0
	// subst replaces the rest of a title that could not be played. The guide is
	// not touched: Plex reloads it rarely, so the channel has to stay on time.
	var subst []store.Program
	// logoAt: where the burned-in logo was at the end of the last item; empty if
	// it was not visible (slate, shrunk credits), then it fades in with the next
	// one. Right after tuning in (logoKnown false) it is simply there.
	var logoAt image.Rectangle
	logoKnown := false
	programAt := func(t time.Time) (*store.Program, bool, error) {
		for i := range subst {
			if subst[i].Start <= t.UnixMilli() && t.UnixMilli() < subst[i].Stop {
				return &subst[i], true, nil
			}
		}
		p, err := s.Program(ctx, ch.ID, t)
		return p, false, err
	}
	for ctx.Err() == nil {
		prog, isSubst, err := programAt(cursor)
		if err != nil {
			log.Error("no program", "err", err)
			return
		}
		offset := cursor.Sub(prog.StartTime())
		remaining := prog.StopTime().Sub(cursor)
		if remaining < time.Second {
			cursor = prog.StopTime()
			continue
		}
		burst := topUp(sent)
		var wake *time.Timer
		// the logo fades out before a slate and where trailers and clips give way
		// to a title or the other way round; it fades in again after that
		logoEnds := false
		if next, _, err := programAt(prog.StopTime()); err == nil {
			logoEnds = logoGroup(next) != logoGroup(prog)
			// Download upcoming remote clips while this item plays.
			if s.Prefetch != nil {
				if url := s.Prefetch(ctx, next); url != "" {
					pf.start(next.RatingKey, url)
				}
			}
			// Touch the next file shortly before it is needed: Plex answers with an
			// error instead of waiting while a sleeping disk spins up.
			if next.Kind != store.KindCard && !strings.HasPrefix(next.RatingKey, store.TrailerPrefix) {
				wake = time.AfterFunc(max(remaining-burst-wakeLead, 0), func() { s.wake(ctx, next, log) })
			}
		}
		started, written := time.Now(), out.n
		label := "Slate"
		if prog.Kind == store.KindCard {
			s.setTitle(sess.ID, label)
			err = s.playCard(ctx, out, ch, prog, remaining, sent, burst, opts, log)
			logoAt, logoKnown = image.Rectangle{}, true
		} else {
			m, merr := s.Media(ctx, prog.RatingKey)
			if merr != nil {
				// Deleted in Plex since planning; the next library sync re-plans the
				// channel. Until then its slot is filled like a failed title.
				err = fmt.Errorf("%w: %s", errMediaMissing, prog.RatingKey)
			} else {
				label = titleOf(m)
				s.setTitle(sess.ID, label)
				local := pf.take(prog.RatingKey)
				o := opts
				o.Caption = s.caption(ctx, ch, prog, opts, log)
				o.LogoFadeIn = logoKnown && logoAt.Empty()
				o.LogoFrom = logoAt
				o.LogoFadeOut = logoEnds
				err = s.play(ctx, out, ch, prog, m, local, offset, remaining, sent, burst, topUp, o, &logoAt, log)
				logoKnown = true
				if logoEnds {
					logoAt = image.Rectangle{}
				}
				if local != "" {
					_ = os.Remove(local)
				}
				if errors.Is(err, errSlowStart) {
					// The source did not deliver in time: the slate fills the slot instead.
					log.Warn("source too slow, showing slate instead", "title", label)
					burst = topUp(sent)
					err = s.playCard(ctx, out, ch, prog, remaining, sent, burst, opts, log)
					logoAt = image.Rectangle{}
				}
			}
		}
		if wake != nil {
			wake.Stop()
		}
		if ctx.Err() != nil || errors.Is(err, errClientGone) {
			if out.n == written && time.Since(started) > silentWait {
				// The client gave up waiting: ffmpeg had not delivered anything.
				log.Warn("client disconnected, the item had not sent anything yet", "title", label, "waited", time.Since(started).Round(time.Second))
			}
			return
		}
		if errors.Is(err, errNotStarted) && retries < len(retryDelays) {
			// Plex sometimes fails to open a file for a moment (disk spinning up, scan
			// running). Try the same position again; the buffer hides the wait.
			log.Warn("source not available, retrying", "title", label, "attempt", retries+1, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelays[retries]):
			}
			retries++
			continue
		}
		retries = 0
		if err != nil {
			failures++
			if failures >= 5 {
				log.Warn("playback failed", "title", label, "err", err)
				return
			}
			// Part of the item may have been sent; assume the most so timestamps never
			// run backwards (a gap is harmless).
			if !errors.Is(err, errNotStarted) && !errors.Is(err, errMediaMissing) {
				part := min(remaining, time.Since(started)+burst+time.Second)
				sent += part
				cursor = cursor.Add(part)
			}
			switch {
			case !isSubst && prog.Kind != store.KindCard && s.Substitute != nil:
				// Fill the rest of the slot with clips or trailers and the slate; the
				// next title starts as announced in the guide.
				log.Warn("playback failed, filling its slot", "title", label, "until", prog.StopTime().Local().Format("15:04"), "err", err)
				subst = s.Substitute(ctx, ch, prog.RatingKey, cursor, prog.StopTime())
			case prog.Kind != store.KindCard:
				// A substitute clip failed: the "up next" slate takes its place. A
				// title without substitutes gets the fault slate.
				log.Warn("playback failed, showing slate instead", "title", label, "err", err)
				card := *prog
				card.Start, card.RatingKey, card.Kind, card.Note = cursor.UnixMilli(), store.CardKey, store.KindCard, ""
				if !isSubst {
					card.RatingKey, card.Note = store.FaultKey, prog.RatingKey
				}
				subst = []store.Program{card}
			default:
				// Even the slate failed: skip it, at worst the stream runs ahead for its length.
				log.Warn("playback failed", "title", label, "err", err)
				sent += prog.StopTime().Sub(cursor)
				cursor = prog.StopTime()
			}
			continue
		}
		failures = 0
		cursor = prog.StopTime()
		sent += remaining
	}
}

// logoGroup sorts programs by how the logo treats them: it stays on through a
// run of trailers and clips (in the corner of the frame) or from title to title,
// and fades between the groups and around slates.
func logoGroup(p *store.Program) string {
	switch {
	case p.Kind == store.KindCard:
		return "card"
	case p.IsMain():
		return "title"
	}
	return "extra"
}

var errClientGone = errors.New("client disconnected")

var errMediaMissing = errors.New("title no longer in Plex")

func titleOf(m *store.Media) string {
	if m.Type == store.TypeTrailer {
		return "Trailer: " + m.Title
	}
	if m.Type == "episode" {
		return fmt.Sprintf("%s – S%02dE%02d %s", m.ShowTitle, m.Season, m.Episode, m.Title)
	}
	return m.Title
}

// caption returns the lower third for a trailer, if enabled and possible.
func (s *Streamer) caption(ctx context.Context, ch *store.Channel, prog *store.Program, opts Options, log *slog.Logger) string {
	if s.Caption == nil || opts.Mode != "full" || !strings.HasPrefix(prog.RatingKey, store.TrailerPrefix) {
		return ""
	}
	path, err := s.Caption(ctx, ch, prog, outHeight(opts))
	if err != nil {
		log.Warn("trailer caption", "err", err)
		return ""
	}
	return path
}

func outHeight(opts Options) int {
	if opts.NormalizeRes == 1080 {
		return 1080
	}
	return 720
}

// play streams a title. logoAt is set to where the logo was at its end (empty
// without the logo, e.g. after shrunk credits).
func (s *Streamer) play(ctx context.Context, out *flushWriter, ch *store.Channel, prog *store.Program, m *store.Media, local string,
	offset, length, tsOffset, burst time.Duration, topUp func(sent time.Duration) time.Duration, opts Options, logoAt *image.Rectangle, log *slog.Logger) error {
	videoIdx, audioIdx := -1, -1
	audioCodec := ""
	input := local
	if input == "" && isRemote(m.PartKey) {
		input = m.PartKey
	}
	if f := store.LocalFile(m.PartKey); input == "" && f != "" {
		input = f // trailer folder
	}
	credits, hasCredits := time.Duration(0), false
	// cardFrom, cardTo: where only the small card shows (credits or the last minute)
	cardFrom, cardTo, hasCard := time.Duration(0), time.Duration(0), false
	// direct: the file read from disk instead of through Plex (plexURL)
	direct, plexURL := "", ""
	if input == "" {
		srv, err := s.Server(ctx)
		if err != nil {
			return err
		}
		input = srv.StreamURL(m.PartKey)
		plexURL = input
		// Fetch stream details to pick the right audio track (language preference).
		// Trailers carry a single audio track in their language anyway.
		if m.Type != store.TypeTrailer {
			if md, err := srv.Item(ctx, m.RatingKey); err == nil && len(md.Media) > 0 && len(md.Media[0].Parts) > 0 {
				parts := md.Media[0].Parts
				if !opts.parted && len(parts) > 1 {
					if durs, ok := partDurations(parts); ok {
						return s.playParts(ctx, out, ch, prog, m, durs, offset, length, tsOffset, burst, topUp, opts, logoAt, log)
					}
				}
				part := parts[0]
				if opts.parted {
					part = parts[min(opts.part, len(parts)-1)]
					input = srv.StreamURL(part.Key)
					plexURL = input
				}
				if s.LocalFile != nil {
					if f := s.LocalFile(ctx, part.File); f != "" {
						direct = f
						input = f
					}
				}
				opts.Aspect = displayAspect(md.Media[0])
				streams := part.Streams
				videoIdx, audioIdx, audioCodec = chooseStreams(streams, opts.AudioLanguages)
				if opts.Mode == "full" {
					if st := videoStream(streams, videoIdx); st != nil && st.HDR() {
						opts.ToneMap = s.toneMap(ctx, st.ColorTransfer)
						log.Debug("HDR to SDR", "title", titleOf(m), "transfer", st.ColorTransfer, "filter", opts.ToneMap != "")
					}
					st, embedded := forcedSubtitle(streams, audioIdx)
					if st != nil {
						opts.Subtitles = s.prepareSubtitles(ctx, srv, st, log.With("title", titleOf(m)))
						if opts.Subtitles != nil {
							log.Debug("burning in forced subtitles", "title", titleOf(m), "codec", st.Codec, "language", st.LanguageCode)
							if opts.Subtitles.Temp {
								defer os.Remove(opts.Subtitles.File)
							}
						}
					}
					// the server does not know the subtitle file yet or did not hand it out
					if opts.Subtitles == nil && direct != "" && (st != nil || embedded) {
						if f := localForcedSubtitle(direct, audioLanguage(streams, audioIdx)); f != "" {
							opts.Subtitles = &Subtitles{Index: -1, File: f}
							log.Debug("burning in forced subtitles from the media folder", "title", titleOf(m), "file", filepath.Base(f))
						}
					}
					if opts.Subtitles == nil && embedded {
						log.Debug("forced subtitles only inside the video, the subtitle script saves them as a file", "title", titleOf(m))
					}
					if opts.Credits && s.Credits != nil {
						credits, hasCredits = creditsStart(md.Markers, time.Duration(m.DurationMs)*time.Millisecond)
						if !hasCredits && s.CreditsCard != nil && (m.Type == "movie" || m.Type == "episode") {
							cardFrom, cardTo, hasCard = cardCredits(md.Markers, time.Duration(m.DurationMs)*time.Millisecond)
						}
					}
				}
			}
		}
	}
	*logoAt = image.Rectangle{}
	if logoOn(opts) {
		w, h := frameSize(opts)
		*logoAt = pictureRect(w, h, opts.Aspect)
	}
	if opts.Mode == "copy" && needsAudioTranscode(audioCodec) {
		// Plex often lacks a decoder for these in Live TV (German Plex says "Ton
		// Decoder 'dts' nicht gefunden"); converting only the audio is cheap.
		log.Debug("converting audio", "title", titleOf(m), "codec", audioCodec)
		opts.Mode = "audio"
	}
	// Only trailers give way to the slate when slow; a movie may just wait for a disk to spin up.
	timeout := time.Duration(0)
	if m.Type == store.TypeTrailer {
		timeout = startTimeout
	}
	runArgs := func(build func(o Options) []string, offset time.Duration) error {
		args := build(opts)
		log.Debug("ffmpeg", "title", titleOf(m), "offset", offset.Round(time.Second), "local", local != "", "direct", input == direct, "args", redact(args))
		err := s.run(ctx, out, args, timeout)
		if errors.Is(err, errNotStarted) && opts.Mode == "full" && opts.VAAPI != "" {
			// The GPU may refuse a stream the test encode accepted; software still works.
			log.Warn("GPU encoding failed, using software for this title", "title", titleOf(m), "err", err)
			opts.VAAPI = ""
			args = build(opts)
			err = s.run(ctx, out, args, timeout)
		}
		if errors.Is(err, errNotStarted) && direct != "" && input == direct {
			// The file could be opened but not read (permissions, mount gone): Plex still can.
			log.Warn("file not readable directly, streaming it from Plex", "title", titleOf(m), "file", direct, "err", err)
			input = plexURL
			args = build(opts)
			err = s.run(ctx, out, args, timeout)
		}
		if errors.Is(err, errNotStarted) && isRemote(input) {
			// ffmpeg only says "5XX"; Plex's own answer usually names the reason.
			err = fmt.Errorf("%w (the server answers: %s)", err, checkSource(ctx, input))
		}
		return err
	}
	plain := func(offset, length, tsOffset, burst time.Duration) error {
		return runArgs(func(o Options) []string {
			return buildArgs(input, offset, length, tsOffset, burst, videoIdx, audioIdx, o)
		}, offset)
	}

	// With credits, the title is played in two parts: up to shortly before the
	// credits as usual, then the credits shrunk next to the panel.
	split := credits - creditsLead
	end := offset + length
	if !hasCredits || end-max(split, offset) < 5*time.Second {
		if !hasCard {
			return plain(offset, length, tsOffset, burst)
		}
		at, parted := cardSplit(cardFrom, offset, length)
		if parted {
			// The card's pages only join in a second part shortly before it:
			// held for most of a film before turning, they stall ffmpeg 7
			// (no output at all).
			fadeOut := opts.LogoFadeOut
			opts.LogoFadeOut = false
			first := at - offset
			if err := plain(offset, first, tsOffset, burst); err != nil {
				return err
			}
			offset, length, tsOffset = at, length-first, tsOffset+first
			burst = topUp(tsOffset)
			// the logo simply stays where it is
			opts.LogoFadeOut, opts.LogoFadeIn, opts.LogoFrom = fadeOut, false, *logoAt
		}
		if c := s.creditsCard(ctx, ch, prog, m, cardFrom, cardTo, offset, length, opts, log); c != nil {
			defer os.Remove(c.List)
			opts.CardOverlay = c
		}
		err := plain(offset, length, tsOffset, burst)
		if parted {
			err = mustNotRetry(err)
		}
		return err
	}
	// the credits fade the logo out themselves, not the part before them
	opts.LogoFadeOut = false
	panel := s.renderCredits(ctx, ch, prog, prog.StartTime().Add(credits), opts, log)
	if split > offset {
		first := split - offset
		if err := plain(offset, first, tsOffset, burst); err != nil {
			return err
		}
		offset, length, tsOffset = split, length-first, tsOffset+first
		burst = topUp(tsOffset)
	}
	var p *CreditsPanel
	select {
	case p = <-panel:
	case <-time.After(creditsWait):
		// Joined just before the credits: waiting longer would drain the buffer.
		log.Warn("credits panel not ready, playing the credits as they are", "title", titleOf(m))
	case <-ctx.Done():
		return ctx.Err()
	}
	if p == nil {
		return mustNotRetry(plain(offset, length, tsOffset, burst))
	}
	at := (credits - offset).Seconds()
	// the clock shows the schedule's time, which is what the viewer sees
	clock, clockStart := "", prog.StartTime().Add(offset)
	var breaks []time.Duration
	if s.Clock != nil {
		_, h := frameSize(opts)
		var err error
		if clock, err = s.clockList(ctx, clockStart, length, h); err != nil {
			log.Warn("credits clock", "err", err)
			clock = ""
		} else {
			defer os.Remove(clock)
			breaks = minuteBreaks(clockStart, length+10*time.Second)
		}
	}
	if credits > offset {
		// the panel changes there: letterbox bars turn into the panel
		breaks = append(breaks, credits-offset)
		slices.Sort(breaks)
	}
	list, err := creditsList(s.CacheDir, p, max(credits+creditsZoom-offset, 0), length, breaks)
	if err != nil {
		log.Warn("credits", "err", err)
		return mustNotRetry(plain(offset, length, tsOffset, burst))
	}
	defer os.Remove(list)
	log.Debug("credits shrunk", "title", titleOf(m), "at", credits.Round(time.Second))
	err = runArgs(func(o Options) []string {
		return buildCreditsArgs(input, offset, length, tsOffset, burst, videoIdx, audioIdx, at, list, clock, o)
	}, offset)
	if errors.Is(err, errNotStarted) {
		log.Warn("credits failed, playing them as they are", "title", titleOf(m), "err", err)
		return mustNotRetry(plain(offset, length, tsOffset, burst))
	}
	*logoAt = image.Rectangle{}
	return err
}

// partDurations returns the lengths of the files of a title stored in several
// (false if one is unknown).
func partDurations(parts []mediaserver.Part) ([]time.Duration, bool) {
	out := make([]time.Duration, len(parts))
	for i, p := range parts {
		if p.Duration <= 0 {
			return nil, false
		}
		out[i] = time.Duration(p.Duration) * time.Millisecond
	}
	return out, true
}

// playParts plays a title stored in several files (stacked in Plex, e.g. CD1
// and CD2) file after file, starting in the one offset falls into. The end
// credits stay as they are: their markers count from the start of the title.
func (s *Streamer) playParts(ctx context.Context, out *flushWriter, ch *store.Channel, prog *store.Program, m *store.Media, durs []time.Duration,
	offset, length, tsOffset, burst time.Duration, topUp func(sent time.Duration) time.Duration, opts Options, logoAt *image.Rectangle, log *slog.Logger) error {
	o := opts
	o.parted, o.Credits = true, false
	end := offset + length
	start, played := time.Duration(0), false
	for i, d := range durs {
		from, to := max(offset, start), min(end, start+d)
		if i == len(durs)-1 {
			to = end // the last file takes what the title's length says is left
		}
		partStart := start
		start += d
		if to <= from {
			continue
		}
		o.part = i
		o.LogoFadeOut = opts.LogoFadeOut && to >= end
		log.Debug("playing part", "title", titleOf(m), "part", i+1, "of", len(durs))
		err := s.play(ctx, out, ch, prog, m, "", from-partStart, to-from, tsOffset, burst, topUp, o, logoAt, log)
		if played {
			err = mustNotRetry(err)
		}
		if err != nil {
			return err
		}
		played = true
		tsOffset += to - from
		burst = topUp(tsOffset)
		// the logo simply stays where it is
		o.LogoFadeIn, o.LogoFrom = false, *logoAt
		if to >= end {
			break
		}
	}
	return nil
}

// mustNotRetry keeps the end of a title from being retried from its start: once
// its first part has been sent, a failure no longer counts as "not started".
func mustNotRetry(err error) error {
	if errors.Is(err, errNotStarted) {
		return errors.New(err.Error())
	}
	return err
}

// creditsCard prepares the small card over the credits from start to end of
// the title, for the part from offset on (nil when they are not in it or the
// card cannot be rendered in time).
func (s *Streamer) creditsCard(ctx context.Context, ch *store.Channel, prog *store.Program, m *store.Media, start, end, offset, length time.Duration, opts Options, log *slog.Logger) *CardOverlay {
	from := (start + cardDelay - offset).Seconds()
	to := (min(end-cardEnd, offset+length) - offset).Seconds()
	if to-max(from, 0) < 3 {
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, creditsWait)
	defer cancel()
	p, err := s.CreditsCard(rctx, ch, prog, prog.StartTime().Add(start), outHeight(opts))
	if err != nil || p == nil || len(p.Pages) == 0 {
		if err != nil && ctx.Err() == nil {
			log.Warn("credits card", "err", err)
		}
		return nil
	}
	list, err := creditsList(s.CacheDir, p, time.Duration(max(from, 0)*float64(time.Second)), length, nil)
	if err != nil {
		log.Warn("credits card", "err", err)
		return nil
	}
	log.Debug("credits card", "title", titleOf(m), "at", start.Round(time.Second))
	return &CardOverlay{List: list, From: from, To: to}
}

// renderCredits renders the panel for credits starting at at in the background;
// the channel yields nil if that fails.
func (s *Streamer) renderCredits(ctx context.Context, ch *store.Channel, prog *store.Program, at time.Time, opts Options, log *slog.Logger) <-chan *CreditsPanel {
	res := make(chan *CreditsPanel, 1)
	go func() {
		w, h := frameSize(opts)
		video := CreditsVideo(w, h)
		pic := pictureRect(w, h, opts.Aspect)
		p, err := s.Credits(ctx, ch, prog, at, video, fitRect(video, float64(pic.Dx())/float64(pic.Dy())), h)
		if err != nil || p == nil || len(p.Pages) == 0 {
			if err != nil && ctx.Err() == nil {
				log.Warn("credits panel", "err", err)
			}
			p = nil
		}
		res <- p
	}()
	return res
}

// checkSource requests the first byte of a source and describes the answer.
func checkSource(ctx context.Context, url string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Range", "bytes=0-0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return mediaserver.Redact(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		return fmt.Sprintf("HTTP %d, reachable now", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	return mediaserver.Redact(fmt.Sprintf("HTTP %d %s", resp.StatusCode, strings.Join(strings.Fields(string(body)), " ")))
}

func isRemote(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// playCard streams the "up next" slate: a short clip of the still, encoded once
// and repeated by copying, plus music or silence (only the audio is encoded live).
func (s *Streamer) playCard(ctx context.Context, out *flushWriter, ch *store.Channel, prog *store.Program, length, tsOffset, burst time.Duration, opts Options, log *slog.Logger) error {
	if s.Card == nil {
		return errors.New("no card renderer")
	}
	path, err := s.Card(ctx, ch, prog, outHeight(opts))
	if err != nil {
		return err
	}
	rate := secs(burst)
	var audioInput []string
	var audioFilter []string
	music, theme := "", false
	if s.CardAudio != nil {
		music, theme = s.CardAudio(ctx, ch, prog)
	}
	switch {
	case theme:
		// played once and faded out with the slate; silence if it ends earlier
		fade := min(themeFade, length)
		audioInput = []string{"-readrate", "1", "-readrate_initial_burst", rate}
		// tuned in during the slate: the theme is already playing, as on TV
		// (past its end there is only silence)
		if at := prog.StopTime().Sub(prog.StartTime()) - length; at > 0 {
			audioInput = append(audioInput, "-ss", secs(at))
		}
		audioInput = append(audioInput, "-i", music)
		audioFilter = []string{"-filter:a", fmt.Sprintf("volume=0.5,apad,afade=t=out:st=%s:d=%s", secs(length-fade), secs(fade))}
	case music != "":
		audioInput = []string{"-readrate", "1", "-readrate_initial_burst", rate, "-stream_loop", "-1", "-i", music}
		audioFilter = []string{"-filter:a", "volume=0.5"}
	default:
		audioInput = []string{"-readrate", "1", "-readrate_initial_burst", rate, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo"}
	}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	// long "up next" slates turn pages (with the time, see pages.go)
	clock := ""
	if total := prog.StopTime().Sub(prog.StartTime()); s.CardPages != nil && prog.RatingKey == store.CardKey && total >= pagedMin {
		pages, err := s.CardPages(ctx, ch, prog, outHeight(opts))
		if err != nil {
			log.Warn("slate pages", "err", err)
		} else if len(pages) > 1 {
			if clock, err = s.pagedCardList(ctx, pages, prog.StartTime(), total, length, burst, outHeight(opts), log); err != nil {
				log.Warn("slate pages", "err", err)
				clock = ""
			} else {
				defer os.Remove(clock)
				log.Debug("slate pages", "pages", len(pages))
			}
		}
	}
	// with the time at the top right, like the credits: a clip per minute
	if s.Clock != nil && clock == "" {
		var err error
		if clock, err = s.cardClockList(ctx, path, prog.StopTime().Add(-length), length, outHeight(opts), log); err != nil {
			log.Warn("slate clock", "err", err)
			clock = ""
		} else {
			defer os.Remove(clock)
		}
	}
	clip, err := "", error(nil)
	if clock == "" {
		clip, err = s.cardClip(ctx, path)
	}
	if clock != "" {
		args = append(args, "-readrate", "1", "-readrate_initial_burst", rate, "-f", "concat", "-safe", "0", "-i", clock)
		args = append(args, audioInput...)
		args = append(args, "-t", secs(length), "-map", "0:v", "-map", "1:a", "-c:v", "copy")
	} else if err == nil {
		args = append(args, "-readrate", "1", "-readrate_initial_burst", rate, "-stream_loop", "-1", "-i", clip)
		args = append(args, audioInput...)
		args = append(args, "-t", secs(length), "-map", "0:v", "-map", "1:a", "-c:v", "copy")
	} else {
		// Encoded live as before; the still is decoded once and repeated.
		log.Warn("slate clip", "err", err)
		args = append(args, "-readrate", "1", "-readrate_initial_burst", rate, "-i", path)
		args = append(args, audioInput...)
		args = append(args, "-t", secs(length), "-filter_complex", "[0:v]"+cardVideoFilter+"[v]", "-map", "[v]", "-map", "1:a")
		args = append(args, cardVideoArgs...)
	}
	args = append(args, audioFilter...)
	args = append(args, "-c:a", "aac", "-b:a", "96k", "-ac", "2")
	if tsOffset > 0 {
		args = append(args, "-output_ts_offset", secs(tsOffset))
	}
	args = append(args, "-f", "mpegts", "-mpegts_flags", "+resend_headers+initial_discontinuity", "-flush_packets", "1", "pipe:1")
	log.Debug("ffmpeg card", "length", length.Round(time.Second), "music", music != "", "theme", theme, "clip", clip != "", "clock", clock != "")
	return s.run(ctx, out, args, 0)
}

// cardVideoFilter turns a still into 25 fps video: converted once, then repeated.
const cardVideoFilter = "format=yuv420p,loop=loop=-1:size=1,fps=25,setpts=N/25/TB"

// cardVideoArgs encode the slate: a GOP is exactly 2s, so the clip repeats
// cleanly from keyframe to keyframe.
var cardVideoArgs = []string{"-c:v", "libx264", "-preset", "ultrafast", "-tune", "stillimage", "-g", "50", "-r", "25"}

// cardClipLength is the slate clip: one GOP.
const cardClipLength = 2 * time.Second

// cardClip returns a 2s MPEG-TS clip of the slate PNG, encoded once and kept
// next to it (same name, .ts), so the cache cleanup removes both together.
func (s *Streamer) cardClip(ctx context.Context, png string) (string, error) {
	return s.encodeCardClip(ctx, strings.TrimSuffix(png, ".png")+".ts", []string{"-i", png}, "[0:v]"+cardVideoFilter+"[v]", false, cardClipLength)
}

// cardClockClip is cardClip with the time text burnt in at the top right corner,
// where the credits panel shows it too (named <slate>-<hhmm>-c3.ts). Its
// timestamps start at 0, so the play list can cut it (outpoint).
func (s *Streamer) cardClockClip(ctx context.Context, png, text string, height int) (string, error) {
	clip := strings.TrimSuffix(png, ".png") + "-" + strings.ReplaceAll(text, ":", "") + clockClipSuffix
	clock, err := s.Clock(ctx, text, height)
	if err != nil {
		// the slate without the time keeps the stream going
		s.Log.Warn("slate clock", "time", text, "err", err)
		return s.encodeCardClip(ctx, clip, []string{"-i", png}, "[0:v]"+cardVideoFilter+"[v]", true, cardClipLength)
	}
	right, top := CardClockMargin(height)
	filter := fmt.Sprintf("[0:v][1:v]overlay=x=W-w-%d:y=%d,%s[v]", right, top, cardVideoFilter)
	return s.encodeCardClip(ctx, clip, []string{"-i", png, "-i", clock}, filter, true, cardClipLength)
}

// clockClipSuffix names the cached slate clips with the time; a new one when
// the clock moves, so clips with the old position are not reused.
const clockClipSuffix = "-c3.ts"

// CardClockMargin is where the clock PNG sits on a slate of the given height:
// its distance from the right and the top edge. It is the corner where the
// credits panel shows the time, so the clock stays put between them.
func CardClockMargin(height int) (right, top int) {
	v, pad := CreditsVideo(height*16/9, height), ClockPad(height)
	return v.Min.X - pad, v.Min.Y - pad
}

// cardClockList writes an ffconcat list of the slate with the time from start on
// for length: per minute its clip, repeated GOP by GOP and cut where the minute
// changes. The clips of the first two minutes are made right away (the minute may
// change within seconds), the others in the background, long before they play.
func (s *Streamer) cardClockList(ctx context.Context, png string, start time.Time, length time.Duration, height int, log *slog.Logger) (string, error) {
	total := length + 10*time.Second // the list has to outlast the segment
	ends := append(minuteBreaks(start, total), total)
	texts := make([]string, len(ends))
	clips := make([]string, len(ends))
	from := time.Duration(0)
	for i := range ends {
		texts[i] = s.clockText(start.Add(from).Local())
		clips[i] = strings.TrimSuffix(png, ".png") + "-" + strings.ReplaceAll(texts[i], ":", "") + clockClipSuffix
		from = ends[i]
	}
	now := min(2, len(ends))
	for i := range now {
		if _, err := s.cardClockClip(ctx, png, texts[i], height); err != nil {
			return "", err
		}
	}
	if len(ends) > now {
		go func() {
			for i := now; i < len(ends) && ctx.Err() == nil; i++ {
				if _, err := s.cardClockClip(ctx, png, texts[i], height); err != nil && ctx.Err() == nil {
					log.Warn("slate clock", "time", texts[i], "err", err)
				}
			}
		}()
	}
	var entries []concatEntry
	from = 0
	for i, to := range ends {
		for at := from; at < to; at += cardClipLength {
			entries = append(entries, concatEntry{clips[i], min(cardClipLength, to-at), true})
		}
		from = to
	}
	return writeConcat(s.CacheDir, entries)
}

// encodeCardClip encodes a slate clip of length d once (usually cardClipLength);
// inputs and filter make its picture.
// zero: timestamps start at 0 instead of after the muxer's usual delay.
func (s *Streamer) encodeCardClip(ctx context.Context, clip string, inputs []string, filter string, zero bool, d time.Duration) (string, error) {
	// marked as used, so the cache cleanup (by modification time) keeps it
	if now := time.Now(); os.Chtimes(clip, now, now) == nil {
		return clip, nil
	}
	tmp := clip + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	args := append([]string{"-hide_banner", "-nostdin", "-loglevel", "error"}, inputs...)
	args = append(args, "-t", secs(d), "-filter_complex", filter, "-map", "[v]")
	args = append(args, cardVideoArgs...)
	if zero {
		args = append(args, "-muxdelay", "0", "-muxpreload", "0")
	}
	args = append(args, "-f", "mpegts", "-y", tmp)
	if b, err := exec.CommandContext(ctx, s.FFmpeg, args...).CombinedOutput(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(b)))
	}
	// renamed into place, so a parallel stream never reads half a clip
	return clip, os.Rename(tmp, clip)
}

// startTimeout is how long a trailer may take to deliver its first data. It stays
// below DefaultBuffer, so the client never runs dry while we wait.
const startTimeout = 8 * time.Second

var errSlowStart = errors.New("source did not start in time")

// errNotStarted: ffmpeg failed before sending anything (e.g. the source could
// not be opened), so the same position can simply be tried again.
var errNotStarted = errors.New("source failed before sending data")

// retryDelays: waits before trying a failed source again. Together they stay
// below DefaultBuffer; after that the slot is filled (Substitute).
var retryDelays = []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}

// silentWait: an item that has sent nothing for this long when the client
// disconnects is logged (the client gave up on it, not the viewer switching).
const silentWait = 5 * time.Second

// wakeLead: how long before an item starts its file is touched (see wake).
const wakeLead = 45 * time.Second

// wake reads the first bytes of a program's file so its disk is spinning when
// playback starts. Failures only cost the head start.
func (s *Streamer) wake(ctx context.Context, prog *store.Program, log *slog.Logger) {
	m, err := s.Media(ctx, prog.RatingKey)
	if err != nil || m.PartKey == "" || isRemote(m.PartKey) || store.LocalFile(m.PartKey) != "" {
		return
	}
	srv, err := s.Server(ctx)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.StreamURL(m.PartKey), nil)
	req.Header.Set("Range", "bytes=0-65535")
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Debug("wake next file", "title", titleOf(m), "err", mediaserver.Redact(err.Error()))
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	log.Debug("wake next file", "title", titleOf(m), "status", resp.StatusCode, "took", time.Since(started).Round(time.Millisecond))
}

// run executes ffmpeg and copies its output to the client. With a timeout,
// ffmpeg is stopped if it produces nothing within that time (errSlowStart).
func (s *Streamer) run(ctx context.Context, out *flushWriter, args []string, timeout time.Duration) error {
	cmd := exec.CommandContext(ctx, s.FFmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}
	var started, timedOut atomic.Bool
	if timeout > 0 {
		t := time.AfterFunc(timeout, func() {
			if !started.Load() {
				timedOut.Store(true)
				_ = cmd.Process.Kill()
			}
		})
		defer t.Stop()
	}
	_, copyErr := io.Copy(out, &firstRead{r: stdout, seen: &started})
	if copyErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errClientGone
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if timedOut.Load() {
			return errSlowStart
		}
		if !started.Load() {
			return fmt.Errorf("%w: ffmpeg: %v: %s", errNotStarted, err, mediaserver.Redact(strings.TrimSpace(stderr.String())))
		}
		return fmt.Errorf("ffmpeg: %v: %s", err, mediaserver.Redact(strings.TrimSpace(stderr.String())))
	}
	return nil
}

// firstRead flags when the first bytes arrive.
type firstRead struct {
	r    io.Reader
	seen *atomic.Bool
}

func (f *firstRead) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if n > 0 {
		f.seen.Store(true)
	}
	return n, err
}

// needsAudioTranscode reports audio codecs Plex Live TV often cannot decode.
func needsAudioTranscode(codec string) bool {
	switch strings.ToLower(codec) {
	case "dts", "dca", "truehd", "mlp":
		return true
	}
	return false
}

// chooseStreams picks the first video stream and the preferred audio stream.
// Returned values are ffmpeg input stream indices (-1 = let ffmpeg decide) and
// the codec of the chosen audio stream.
func chooseStreams(streams []mediaserver.Stream, langs []string) (int, int, string) {
	video := -1
	var audios []mediaserver.Stream
	for _, st := range streams {
		switch st.StreamType {
		case 1:
			if video < 0 {
				video = st.Index
			}
		case 2:
			audios = append(audios, st)
		}
	}
	for _, lang := range langs {
		for _, a := range audios {
			if strings.EqualFold(a.LanguageCode, lang) {
				return video, a.Index, a.Codec
			}
		}
	}
	for _, a := range audios {
		if a.Selected {
			return video, a.Index, a.Codec
		}
	}
	for _, a := range audios {
		if a.Default {
			return video, a.Index, a.Codec
		}
	}
	if len(audios) > 0 {
		return video, audios[0].Index, audios[0].Codec
	}
	return video, -1, ""
}

func secs(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }

// sourceArgs opens the source at offset (input 0), read in real time; with
// gpu the encoder (and with opts.GPUDecode the decoder) is the GPU.
func sourceArgs(input string, offset, burst time.Duration, gpu bool, opts Options) []string {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error",
		"-fflags", "+genpts+discardcorrupt"}
	if isRemote(input) {
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5")
	}
	if offset > time.Second {
		args = append(args, "-ss", secs(offset))
	}
	if gpu && opts.GPUEncoder.VAAPI() {
		args = append(args, "-init_hw_device", "vaapi=va:"+opts.VAAPI, "-filter_hw_device", "va")
		if opts.GPUDecode {
			// Frames come back to memory for the overlays. Codecs the GPU cannot
			// decode (old MPEG-4 …) fall back to software by themselves.
			args = append(args, "-hwaccel", "vaapi", "-hwaccel_device", "va")
		}
	}
	// Read in real time. This keeps CPU, disk and network usage at the bitrate of the content.
	return append(args, "-readrate", "1", "-readrate_initial_burst", secs(burst), "-i", input)
}

// frameSize is the output picture size in full mode.
func frameSize(opts Options) (int, int) {
	h := outHeight(opts)
	return h * 16 / 9, h
}

// scaleFilter fits the picture of stream in into the output frame (letterboxed)
// with the given swscale flags.
func scaleFilter(in string, w, h int, flags string) string {
	return fmt.Sprintf("[%s]scale=%d:%d:force_original_aspect_ratio=decrease:flags=%s,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1", in, w, h, flags, w, h)
}

// encodeTail finishes a full mode filter graph: it hands the frames to the encoder.
func encodeTail(gpu bool, enc GPUEncoder) string {
	// Decoding, scaling and overlays stay in software (any source works);
	// with a GPU only the expensive encoding moves there. NVENC and the V4L2
	// encoder (Raspberry Pi) take frames from memory, VAAPI from the GPU.
	if gpu && enc.VAAPI() {
		return ",format=nv12,hwupload[v]"
	}
	return ",format=yuv420p[v]"
}

// encodeArgs are the codec settings of full mode; preset is the libx264
// preset of software encoding.
func encodeArgs(opts Options, gpu bool, preset string) []string {
	_, h := frameSize(opts)
	var args []string
	if gpu {
		args = opts.GPUEncoder.args(h)
	} else {
		args = []string{"-c:v", "libx264", "-preset", preset, "-crf", "21", "-maxrate", maxrate(h),
			"-bufsize", bufsize(h), "-g", "50", "-profile:v", "high"}
	}
	return append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2", "-ar", "48000")
}

// Many files end their audio a few seconds before the picture (or the other
// way round). Plex closes such a gap when converting the stream, which leaves
// the next titles that far out of sync until the channel is tuned in again.
// Wherever it is encoded anyway, the shorter one is filled up to the end of the
// title: the audio with silence, the picture with its last frame (at most
// padMax, in case the duration Plex gives is far off).
const padMax = 30

var (
	audioPad = fmt.Sprintf("apad=pad_dur=%d", padMax)
	videoPad = fmt.Sprintf(",tpad=stop_mode=clone:stop_duration=%d", padMax)
)

// outputArgs map the audio track and write MPEG-TS continuing at tsOffset.
func outputArgs(audioIdx int, tsOffset time.Duration) []string {
	var args []string
	// Video is always mapped first, audio second, so stream order stays identical across items.
	if audioIdx >= 0 {
		args = append(args, "-map", "0:"+strconv.Itoa(audioIdx))
	} else {
		args = append(args, "-map", "0:a:0?")
	}
	args = append(args, "-sn", "-dn")
	// Continuous timestamps across items so Plex sees one uninterrupted stream.
	if tsOffset > 0 {
		args = append(args, "-output_ts_offset", secs(tsOffset))
	}
	return append(args, "-max_muxing_queue_size", "1024", "-f", "mpegts",
		"-mpegts_flags", "+resend_headers+initial_discontinuity", "-flush_packets", "1", "pipe:1")
}

func buildArgs(input string, offset, length, tsOffset, burst time.Duration, videoIdx, audioIdx int, opts Options) []string {
	gpu := opts.Mode == "full" && opts.VAAPI != ""
	args := sourceArgs(input, offset, burst, gpu, opts)
	wm := logoOn(opts)
	if wm {
		args = append(args, watermarkInput(opts)...)
	}
	var windows [][2]float64
	card := false
	switch {
	case opts.Mode != "full":
	case opts.Caption != "":
		windows = captionWindows(offset, length)
	case opts.CardOverlay != nil:
		windows, card = [][2]float64{{opts.CardOverlay.From, opts.CardOverlay.To}}, true
	}
	if len(windows) > 0 {
		if card {
			args = append(args, "-f", "concat", "-safe", "0", "-i", opts.CardOverlay.List)
		} else {
			// looped, so the caption can fade in and out over time
			args = append(args, "-loop", "1", "-framerate", "25", "-t", secs(length), "-i", opts.Caption)
		}
	}
	args = append(args, "-t", secs(length))

	videoIn := "0:v:0"
	if videoIdx >= 0 {
		videoIn = "0:" + strconv.Itoa(videoIdx)
	}
	if opts.Mode != "full" {
		args = append(args, "-map", videoIn)
	}
	switch opts.Mode {
	case "full":
		w, h := frameSize(opts)
		pre := ""
		if opts.Subtitles != nil {
			pre = subtitleFilter(opts.Subtitles, videoIn, "sub", offset) + ";"
			videoIn = "sub"
		}
		// HDR becomes SDR after scaling: much cheaper at the output size than at 4K
		base := pre + scaleFilter(videoIn, w, h, "bicubic") + opts.ToneMap + videoPad
		out := encodeTail(gpu, opts.GPUEncoder)
		graph := base + out
		if wm || len(windows) > 0 {
			cur := "base"
			graph = base + "[base]"
			if wm {
				graph += ";" + logoFilters(opts, "1:v", w, h, length)
				cur = "wmd"
			}
			if len(windows) > 0 {
				in := "1:v"
				if wm {
					in = "2:v"
				}
				if card {
					// the pages change rarely; the fades need every frame
					graph += fmt.Sprintf(";[%s]fps=%d[card]", in, CreditsFPS)
					in = "card"
				}
				var c string
				c, cur = captionFilter(in, cur, windows, w, h)
				graph += ";" + c
			}
			graph += ";[" + cur + "]null" + out
		}
		args = append(args, "-filter_complex", graph, "-map", "[v]")
		args = append(args, encodeArgs(opts, gpu, "veryfast")...)
		args = append(args, "-filter:a", audioPad)
	case "audio":
		args = append(args, "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ac", "2", "-ar", "48000", "-filter:a", audioPad)
	default:
		args = append(args, "-c", "copy")
	}
	return append(args, outputArgs(audioIdx, tsOffset)...)
}

// watermarkFilter scales the logo to its target width and applies the opacity;
// extra filters (fades) follow, before the label [wm].
func watermarkFilter(wm *Watermark, frameW int, input, extra string) string {
	width := frameW * wm.Size / 100
	return fmt.Sprintf("[%s]scale=%d:-1,format=rgba,colorchannelmixer=aa=%.2f%s[wm]", input, width, float64(wm.Opacity)/100, extra)
}

// logoFadeIn: how long the logo takes to fade in (Options.LogoFadeIn), after a short pause.
const (
	logoFadeDelay = 0.3
	logoFadeIn    = 1.0
)

// logoFadeOut: how long the logo takes to fade out at the end of an item (Options.LogoFadeOut).
const logoFadeOut = 1.0

// watermarkInput adds the logo as an input: a single still, or looped for a
// moment so it can fade in (after that the overlay keeps its last frame). To
// fade out at the end it is repeated in the filter instead (watermarkFadeOut).
func watermarkInput(opts Options) []string {
	if (opts.LogoFadeIn || logoMoves(opts)) && !opts.LogoFadeOut {
		return []string{"-loop", "1", "-framerate", "25", "-t", secs(time.Duration((logoFadeDelay + logoFadeIn + 0.5) * float64(time.Second))), "-i", opts.Watermark.Path}
	}
	return []string{"-i", opts.Watermark.Path}
}

// watermarkFade is the fade-in of the logo, if wanted.
func watermarkFade(opts Options) string {
	if !opts.LogoFadeIn {
		return ""
	}
	return fmt.Sprintf(",fade=t=in:st=%.2f:d=%.2f:alpha=1", logoFadeDelay, logoFadeIn)
}

// logoRepeat repeats the converted logo at 25 fps for the whole item, so it can
// fade out at the end (only the still is scaled; the overlay ends with the title).
const logoRepeat = ",loop=loop=-1:size=1,setpts=N/25/TB"

// watermarkFadeOut fades the (repeated) logo out over the last second of length.
func watermarkFadeOut(length time.Duration) string {
	d := min(logoFadeOut, length.Seconds())
	return fmt.Sprintf(",fade=t=out:st=%.2f:d=%.2f:alpha=1", length.Seconds()-d, d)
}

// logoOn reports whether the logo is burned in (full mode only).
func logoOn(opts Options) bool {
	return opts.Mode == "full" && opts.Watermark != nil && opts.Watermark.Path != ""
}

// logoMoves reports whether the logo lands elsewhere than at the end of the
// previous item (another picture format), so it has to move by fading.
func logoMoves(opts Options) bool {
	w, h := frameSize(opts)
	return !opts.LogoFrom.Empty() && opts.LogoFrom != pictureRect(w, h, opts.Aspect)
}

// logoMove: the logo at its old place fades out this fast while it fades in at the new one.
const logoMove = 0.6

// logoFilters puts the logo onto [base] as [wmd]: faded in if it was not
// visible before, moved by fading if it was elsewhere (the old one fades out
// while the new one fades in), faded out at the end if a slate follows.
func logoFilters(opts Options, input string, w, h int, length time.Duration) string {
	wm, pic := opts.Watermark, pictureRect(w, h, opts.Aspect)
	extra, out := "", ""
	if opts.LogoFadeOut {
		extra, out = logoRepeat, watermarkFadeOut(length)
	}
	if !logoMoves(opts) {
		return watermarkFilter(wm, w, input, extra+watermarkFade(opts)+out) + ";[base][wm]" + watermarkOverlay(wm, w, pic) + "[wmd]"
	}
	return watermarkFilter(wm, w, input, extra) + ";[wm]split[wmn][wmo]" +
		fmt.Sprintf(";[wmo]fade=t=out:st=0:d=%.2f:alpha=1[wmold]", logoMove) +
		fmt.Sprintf(";[wmn]fade=t=in:st=%.2f:d=%.2f:alpha=1%s[wmnew]", logoFadeDelay, logoFadeIn, out) +
		";[base][wmold]" + watermarkOverlay(wm, w, opts.LogoFrom) + "[wmb]" +
		";[wmb][wmnew]" + watermarkOverlay(wm, w, pic) + "[wmd]"
}

// watermarkOverlay places the logo in a corner of the picture pic (inside
// letterbox or pillarbox bars) with a margin of 3% of the frame width.
func watermarkOverlay(wm *Watermark, frameW int, pic image.Rectangle) string {
	m := frameW * 3 / 100
	x, y := fmt.Sprint(pic.Min.X+m), fmt.Sprint(pic.Min.Y+m)
	if strings.HasSuffix(wm.Position, "r") {
		x = fmt.Sprintf("%d-w", pic.Max.X-m)
	}
	if strings.HasPrefix(wm.Position, "b") {
		y = fmt.Sprintf("%d-h", pic.Max.Y-m)
	}
	return fmt.Sprintf("overlay=%s:%s", x, y)
}

// displayAspect returns the display aspect ratio Plex reports for a file (0 = unknown).
func displayAspect(mi mediaserver.MediaInfo) float64 {
	if mi.AspectRatio > 0 {
		return mi.AspectRatio
	}
	if mi.Width > 0 && mi.Height > 0 {
		return float64(mi.Width) / float64(mi.Height)
	}
	return 0
}

// pictureRect returns where a picture of the given display aspect ratio lies
// in a w × h frame: letterboxed or pillarboxed, or the whole frame when the
// ratio is unknown or (almost) the frame's.
func pictureRect(w, h int, aspect float64) image.Rectangle {
	frame := float64(w) / float64(h)
	if aspect <= 0 || math.Abs(aspect-frame) < 0.03 {
		return image.Rect(0, 0, w, h)
	}
	return fitRect(image.Rect(0, 0, w, h), aspect)
}

// fitRect returns the largest rectangle of the given aspect ratio centred in
// box, with even sides.
func fitRect(box image.Rectangle, aspect float64) image.Rectangle {
	bw, bh := box.Dx(), box.Dy()
	w, h := bw, int(math.Round(float64(bw)/aspect))/2*2
	if h > bh {
		w, h = int(math.Round(float64(bh)*aspect))/2*2, bh
	}
	x, y := box.Min.X+(bw-w)/2, box.Min.Y+(bh-h)/2
	return image.Rect(x, y, x+w, y+h)
}

// captionFade is how long the lower third takes to fade and slide in or out.
const captionFade = 0.6

// captionWindows returns when the lower third is visible, in seconds relative
// to the output start: for the whole trailer apart from its first and last
// moments. offset is how far into the trailer playback starts, length what is
// left of it.
func captionWindows(offset, length time.Duration) [][2]float64 {
	total := (offset + length).Seconds()
	if total < 6 {
		return nil
	}
	a, b := 1.5-offset.Seconds(), total-1.5-offset.Seconds()
	if b-max(a, 0) < 2 {
		return nil // (almost) over when joining mid-trailer
	}
	return [][2]float64{{a, b}}
}

// captionFilter overlays the lower third (input in) on stream cur during the
// windows: it fades in while sliding in from the left, and fades out again.
// The PNG has a transparent margin (logo.CaptionPad) that is compensated here.
func captionFilter(in, cur string, windows [][2]float64, frameW, frameH int) (string, string) {
	pad := frameH / 30
	x0, y := frameW*4/100-pad, fmt.Sprintf("H-h-%d", frameH*7/100-pad)
	slide := frameW * 3 / 100
	parts := []string{fmt.Sprintf("[%s]format=rgba,split=%d%s", in, len(windows), labels("cap", len(windows)))}
	for i, win := range windows {
		a, b := win[0], win[1]
		f := fmt.Sprintf("cap%d", i)
		parts = append(parts, fmt.Sprintf("[%s]fade=t=in:st=%.2f:d=%.2f:alpha=1,fade=t=out:st=%.2f:d=%.2f:alpha=1[%sf]",
			f, max(a, 0), captionFade, b-captionFade, captionFade, f))
		x := fmt.Sprintf("'%d-%d*pow(1-min(1,max(0,(t-%.2f)/%.2f)),3)'", x0, slide, a, captionFade)
		next := fmt.Sprintf("capd%d", i)
		parts = append(parts, fmt.Sprintf("[%s][%sf]overlay=x=%s:y=%s:enable='between(t,%.2f,%.2f)':eof_action=pass[%s]",
			cur, f, x, y, max(a, 0), b, next))
		cur = next
	}
	return strings.Join(parts, ";"), cur
}

func labels(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "[%s%d]", prefix, i)
	}
	return b.String()
}

func redact(args []string) string {
	return mediaserver.Redact(strings.Join(args, " "))
}

type flushWriter struct {
	w  io.Writer
	rc *http.ResponseController
	n  int64 // bytes written
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	f.n += int64(n)
	if err == nil {
		err = f.rc.Flush()
	}
	return n, err
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	q := p
	if len(q) > l.n {
		q = q[:l.n]
	}
	l.n -= len(q)
	_, _ = l.w.Write(q)
	return len(p), nil
}

func (s *Streamer) clockText(t time.Time) string {
	if s.ClockText != nil {
		return s.ClockText(t)
	}
	return t.Format("15:04")
}
