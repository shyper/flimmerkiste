package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/logo"
	"github.com/shyper/flimmerkiste/internal/schedule"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/internal/stream"
)

// ---- HDHomeRun emulation: lets Plex add us as a network tuner ----

func (s *Server) deviceID(ctx context.Context) string {
	id := strings.ToUpper(s.instanceID(ctx))
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

func (s *Server) friendlyName(ctx context.Context) string {
	return s.store.Setting(ctx, store.SetFriendlyName, "Flimmerkiste")
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	base := s.baseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"FriendlyName":    s.friendlyName(ctx),
		"Manufacturer":    "Silicondust",
		"ModelNumber":     "HDTC-2US",
		"FirmwareName":    "hdhomeruntc_atsc",
		"FirmwareVersion": "20200101",
		"DeviceID":        s.deviceID(ctx),
		"DeviceAuth":      "flimmerkiste",
		"TunerCount":      s.store.SettingInt(ctx, store.SetTunerCount, 4),
		"BaseURL":         base,
		"LineupURL":       base + "/lineup.json",
	})
}

func (s *Server) handleDeviceXML(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <URLBase>%s</URLBase>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>
    <friendlyName>%s</friendlyName>
    <manufacturer>Silicondust</manufacturer>
    <modelName>HDTC-2US</modelName>
    <modelNumber>HDTC-2US</modelNumber>
    <serialNumber>%s</serialNumber>
    <UDN>uuid:%s</UDN>
  </device>
</root>`, xmlEscape(s.baseURL(r)), xmlEscape(s.friendlyName(ctx)), s.deviceID(ctx), s.deviceID(ctx))
}

func (s *Server) handleLineupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ScanInProgress": 0, "ScanPossible": 1, "Source": "Cable", "SourceList": []string{"Cable"},
	})
}

func (s *Server) handleLineup(w http.ResponseWriter, r *http.Request) {
	chans, err := s.store.ListChannels(r.Context())
	if err != nil {
		s.failErr(w, r, http.StatusInternalServerError, err)
		return
	}
	base := s.baseURL(r)
	prefix := s.store.Setting(r.Context(), store.SetEPGPrefix, "")
	out := []map[string]any{}
	for _, c := range chans {
		if !c.Enabled {
			continue
		}
		out = append(out, map[string]any{
			"GuideNumber": guideNumber(prefix, c.Number),
			"GuideName":   c.Name,
			"URL":         fmt.Sprintf("%s/stream/%d", base, c.Number),
			"HD":          1,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// guideNumber is a channel's id towards Plex: lineup.json, tuning and XMLTV.
// The optional prefix keeps it apart from other tuners and EPG sources.
func guideNumber(prefix string, number int) string {
	return prefix + strconv.Itoa(number)
}

// transcodeMode resolves a channel's conversion; "default" follows the global setting.
func (s *Server) transcodeMode(ctx context.Context, mode string) string {
	if mode == "default" {
		return s.defaultTranscode(ctx)
	}
	return mode
}

func (s *Server) defaultTranscode(ctx context.Context) string {
	switch m := s.store.Setting(ctx, store.SetTranscode, "copy"); m {
	case "audio", "full":
		return m
	}
	return "copy"
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raw := r.PathValue("number")
	if raw == "" {
		// HDHomeRun-style tuning uses the GuideNumber, which may carry the EPG prefix.
		raw = strings.TrimPrefix(r.PathValue("vnumber"), "v")
		raw = strings.TrimPrefix(raw, s.store.Setting(ctx, store.SetEPGPrefix, ""))
	}
	num, err := strconv.Atoi(raw)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ch, err := s.store.GetChannelByNumber(ctx, num)
	if err != nil || !ch.Enabled {
		http.NotFound(w, r)
		return
	}
	if max := s.store.SettingInt(ctx, store.SetTunerCount, 4); len(s.streamer.Sessions()) >= max {
		http.Error(w, "all tuners busy", http.StatusServiceUnavailable)
		return
	}
	cfg, _ := schedule.ParseConfig(ch.Config)
	var langs []string
	for _, l := range strings.Split(s.store.Setting(ctx, store.SetAudioLanguages, "deu,ger"), ",") {
		if l = strings.TrimSpace(l); l != "" {
			langs = append(langs, l)
		}
	}
	wm := cfg.Watermark
	mode := s.transcodeMode(ctx, cfg.Transcode)
	gpu, enc, decode := "", stream.GPUEncoder{}, false
	if mode == "full" && s.store.Setting(ctx, store.SetHWAccel, "auto") != "off" {
		if ok, _ := s.vaapi.Available(ctx); ok {
			gpu, enc = s.vaapi.Path(), s.vaapi.Encoder()
			if s.store.Setting(ctx, store.SetHWDecode, "auto") != "off" {
				decode, _ = s.vaapi.Decode(ctx)
			}
		}
	}
	s.streamer.Serve(w, r, ch, stream.Options{
		Mode:           mode,
		NormalizeRes:   s.store.SettingInt(ctx, store.SetNormalizeRes, 720),
		AudioLanguages: langs,
		Watermark:      s.watermark(ch, wm.Enabled && mode == "full", wm.Position, wm.Opacity, wm.Size, wm.Tone),
		Buffer:         time.Duration(s.store.SettingInt(ctx, store.SetStreamBuffer, int(stream.DefaultBuffer/time.Second))) * time.Second,
		VAAPI:          gpu,
		GPUEncoder:     enc,
		GPUDecode:      decode,
		Credits:        mode == "full" && s.creditsMode(ctx, cfg.Credits) == "squeeze",
	})
}

// ---- XMLTV guide ----

type xmltvDoc struct {
	XMLName    xml.Name         `xml:"tv"`
	Generator  string           `xml:"generator-info-name,attr"`
	Channels   []xmltvChannel   `xml:"channel"`
	Programmes []xmltvProgramme `xml:"programme"`
}

type xmltvChannel struct {
	ID    string     `xml:"id,attr"`
	Names []string   `xml:"display-name"`
	Icon  *xmltvIcon `xml:"icon,omitempty"`
	LCN   string     `xml:"lcn,omitempty"`
}

type xmltvIcon struct {
	Src string `xml:"src,attr"`
}

type xmltvText struct {
	Lang  string `xml:"lang,attr,omitempty"`
	Value string `xml:",chardata"`
}

type xmltvEpisodeNum struct {
	System string `xml:"system,attr"`
	Value  string `xml:",chardata"`
}

type xmltvRating struct {
	System string `xml:"system,attr,omitempty"`
	Value  string `xml:"value"`
}

type xmltvProgramme struct {
	Start       string            `xml:"start,attr"`
	Stop        string            `xml:"stop,attr"`
	Channel     string            `xml:"channel,attr"`
	Title       xmltvText         `xml:"title"`
	SubTitle    *xmltvText        `xml:"sub-title,omitempty"`
	Desc        *xmltvText        `xml:"desc,omitempty"`
	Date        string            `xml:"date,omitempty"`
	Categories  []xmltvText       `xml:"category"`
	Icon        *xmltvIcon        `xml:"icon,omitempty"`
	EpisodeNums []xmltvEpisodeNum `xml:"episode-num"`
	Rating      *xmltvRating      `xml:"rating,omitempty"`
}

const xmltvTime = "20060102150405 -0700"

func (s *Server) handleXMLTV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chans, err := s.store.ListChannels(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	base := s.baseURL(r)
	lang := s.lang(ctx)
	now := s.now()
	doc := xmltvDoc{Generator: "Flimmerkiste"}
	prefix := s.store.Setting(ctx, store.SetEPGPrefix, "")
	for _, c := range chans {
		if !c.Enabled {
			continue
		}
		// The GuideNumber from lineup.json appears as id, display-name and lcn, so
		// Plex finds the channel (like ErsatzTV and Cabernet). The name comes
		// first because Plex shows the first display-name.
		id := guideNumber(prefix, c.Number)
		doc.Channels = append(doc.Channels, xmltvChannel{
			ID: id, Names: []string{c.Name, id + " " + c.Name, id},
			Icon: &xmltvIcon{Src: base + s.logoPath(ctx, c)}, LCN: id,
		})
		from, to := now.Add(-2*time.Hour), now.Add(time.Duration(s.cfg.Horizon)*time.Hour)
		if cfg, err := schedule.ParseConfig(c.Config); err == nil && cfg.FillerOnly(c.Kind) {
			doc.Programmes = append(doc.Programmes, noInfoProgrammes(lang, id, base+s.logoPath(ctx, c), from, to)...)
			continue
		}
		progs := s.mainPrograms(ctx, c.ID, from, to)
		for _, p := range progs {
			m := s.lib.Get(ctx, p.RatingKey)
			if m == nil {
				continue
			}
			doc.Programmes = append(doc.Programmes, programme(lang, base, id, &p, m))
		}
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	io.WriteString(w, xml.Header)
	io.WriteString(w, `<!DOCTYPE tv SYSTEM "xmltv.dtd">`+"\n")
	enc := xml.NewEncoder(w)
	enc.Indent("", " ")
	_ = enc.Encode(doc)
}

// noInfoBlockHours is the length of the placeholder entries of a channel
// without titles of its own (Plex does not offer whole-day entries for mapping).
const noInfoBlockHours = 6

// noInfoBlocks returns the blocks overlapping [from, to): 0:00–6:00, 6:00–12:00 …
// local time (5 or 7 hours across a daylight saving switch).
func noInfoBlocks(from, to time.Time) [][2]time.Time {
	var out [][2]time.Time
	y, m, d := from.Local().Date()
	h := from.Local().Hour() / noInfoBlockHours * noInfoBlockHours
	for start := time.Date(y, m, d, h, 0, 0, 0, time.Local); start.Before(to); {
		h += noInfoBlockHours
		stop := time.Date(y, m, d, h, 0, 0, 0, time.Local)
		out = append(out, [2]time.Time{start, stop})
		start = stop
	}
	return out
}

// noInfoProgrammes covers a channel without titles of its own with placeholder
// blocks. Plex did not offer the channel for mapping while all entries were
// identical and undated, so each block is named after its start and dated.
func noInfoProgrammes(l i18n.Lang, channel, icon string, from, to time.Time) []xmltvProgramme {
	var out []xmltvProgramme
	for _, b := range noInfoBlocks(from, to) {
		title := i18n.T(l, "guide.noInfoAt", i18n.ShortDateTime(l, b[0]))
		out = append(out, xmltvProgramme{Start: b[0].Format(xmltvTime), Stop: b[1].Format(xmltvTime), Channel: channel,
			Title: xmltvText{Lang: string(l), Value: title}, Desc: &xmltvText{Lang: string(l), Value: i18n.T(l, "guide.noInfo")},
			Date: b[0].Format("20060102"), Categories: []xmltvText{{Lang: "en", Value: "Movie"}}, Icon: &xmltvIcon{Src: icon}})
	}
	return out
}

func programme(l i18n.Lang, base, channel string, p *store.Program, m *store.Media) xmltvProgramme {
	x := xmltvProgramme{
		Start:   p.StartTime().Format(xmltvTime),
		Stop:    p.StopTime().Format(xmltvTime),
		Channel: channel,
	}
	desc := m.Summary
	if p.Note != "" {
		desc = strings.TrimSpace("💡 " + p.Note + "\n\n" + desc)
	}
	if desc != "" {
		x.Desc = &xmltvText{Lang: string(l), Value: desc}
	}
	if m.Year > 0 {
		x.Date = strconv.Itoa(m.Year)
	}
	if m.Type == "episode" {
		x.Title = xmltvText{Lang: string(l), Value: m.ShowTitle}
		x.SubTitle = &xmltvText{Lang: string(l), Value: m.Title}
		x.EpisodeNums = []xmltvEpisodeNum{
			{System: "xmltv_ns", Value: fmt.Sprintf("%d.%d.", max(m.Season-1, 0), max(m.Episode-1, 0))},
			{System: "onscreen", Value: fmt.Sprintf("S%02dE%02d", m.Season, m.Episode)},
		}
		x.Categories = append(x.Categories, xmltvText{Lang: "en", Value: "Series"})
		x.Icon = &xmltvIcon{Src: fmt.Sprintf("%s/img/%s/show", base, m.RatingKey)}
	} else {
		x.Title = xmltvText{Lang: string(l), Value: m.Title}
		x.Categories = append(x.Categories, xmltvText{Lang: "en", Value: "Movie"})
		x.Icon = &xmltvIcon{Src: fmt.Sprintf("%s/img/%s/thumb", base, m.RatingKey)}
	}
	for _, g := range m.Tags.Genre {
		x.Categories = append(x.Categories, xmltvText{Lang: string(l), Value: g})
	}
	if m.ContentRating != "" {
		x.Rating = &xmltvRating{Value: m.ContentRating}
	}
	return x
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// ---- logos ----

// logoPath returns the public URL path of a channel logo, versioned so Plex refetches on change.
func (s *Server) logoPath(ctx context.Context, c *store.Channel) string {
	if file, _ := s.activeLogo(c); file != "" {
		return "/logos/" + file
	}
	return fmt.Sprintf("/logos/auto-%d.png?v=%s", c.ID, s.autoLogoVersion(c))
}

// activeLogo returns the uploaded picture a channel shows right now, or "" and the
// options of its automatic logo. During a season a seasonal channel keeps its own
// logo (with the season's symbol; the automatic style follows the season) unless the
// season picks a generated style.
func (s *Server) activeLogo(c *store.Channel) (string, logo.Options) {
	cfg, _ := schedule.ParseConfig(c.Config)
	now := s.now()
	o := logo.Options{Name: c.Name, Style: cfg.LogoStyle, Color: c.Color, Hint: cfg.LogoHint(),
		Seed: cfg.LogoSeed, Hue: cfg.LogoHue, Tagline: cfg.LogoTagline}
	if cfg.LogoNumber {
		o.Number = c.Number
	}
	var season *schedule.Season
	if c.Kind == schedule.KindSeasonal {
		season = cfg.SeasonAt(now)
	}
	if season == nil {
		if cfg.LogoHoliday {
			o.Holiday = logo.HolidayAt(now.Local())
		}
		return c.Logo, o
	}
	file := c.Logo
	o.Hint = season.LogoHint() + " " + o.Hint
	if season.LogoStyle != "" {
		// a generated logo in the season's style; the hue is tuned to the channel's style, so it is left out
		file, o.Style, o.Hue, o.Hint = "", season.LogoStyle, 0, season.LogoHint()
	}
	if cfg.LogoHoliday {
		// the symbol of the running season; without one the calendar decides
		if o.Symbol = strings.TrimSpace(season.Emoji); o.Symbol == "" {
			o.Holiday = logo.HolidayAt(now.Local())
		}
	}
	return file, o
}

// autoLogoVersion changes whenever the logo would change (including holidays and
// seasons), so Plex fetches the new image.
func (s *Server) autoLogoVersion(c *store.Channel) string {
	file, o := s.activeLogo(c)
	style := logo.ResolveStyle(o.Style, o.Name, o.Hint)
	key := fmt.Sprintf("%s|%s|%s|%d|%d|%s|%d|%s|%s|%s|v3", o.Name, style, o.Color, o.Seed, o.Hue, o.Tagline, o.Number, o.Holiday, o.Symbol, file)
	h := sha1.Sum([]byte(key))
	return hex.EncodeToString(h[:6])
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := r.PathValue("file")
	if strings.HasPrefix(file, "auto-") {
		id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(file, "auto-"), ".png"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		c, err := s.store.GetChannel(ctx, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data, err := s.renderLogoCached(c)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(data)
		return
	}
	if file != filepath.Base(file) || strings.HasPrefix(file, ".") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filepath.Join(s.cfg.DataDir, "logos", file))
}

// renderLogoCached renders an automatic logo once and keeps it on disk.
func (s *Server) renderLogoCached(c *store.Channel) ([]byte, error) {
	path := filepath.Join(s.cfg.DataDir, "cache", "logo-"+s.autoLogoVersion(c)+".png")
	if data, err := os.ReadFile(path); err == nil {
		keepCached(path)
		return data, nil
	}
	s.logoMu.Lock()
	defer s.logoMu.Unlock()
	_, o := s.activeLogo(c)
	data, err := logo.Render(o)
	if err != nil {
		return nil, err
	}
	_ = writeFileAtomic(path, data)
	return data, nil
}

// ---- artwork proxy (keeps the Plex token private) ----

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m := s.lib.Get(ctx, r.PathValue("key"))
	if m == nil && strings.HasPrefix(r.PathValue("key"), store.TrailerPrefix) {
		m, _ = s.playable(ctx, r.PathValue("key"))
	}
	if m == nil {
		http.NotFound(w, r)
		return
	}
	path, width, height := m.Thumb, 400, 600
	switch r.PathValue("kind") {
	case "show":
		path = firstNonEmpty(m.ShowThumb, m.Thumb)
	case "art":
		path, width, height = m.Art, 1280, 720
	case "still":
		width, height = 640, 360
	}
	if path == "" {
		http.NotFound(w, r)
		return
	}
	u, err := s.imageURL(ctx, path, width, height)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "image unavailable", resp.StatusCode)
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "public, max-age=604800")
	io.Copy(w, resp.Body)
}
