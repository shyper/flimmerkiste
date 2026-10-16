package jellyfin

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

var _ mediaserver.LiveTVSetup = (*Client)(nil)

func parseUnix(s string) int64 {
	if s == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.0000000Z", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

type liveTVOptions struct {
	TunerHosts []struct {
		ID   string `json:"Id"`
		Type string `json:"Type"`
		URL  string `json:"Url"`
	} `json:"TunerHosts"`
	ListingProviders []struct {
		ID   string `json:"Id"`
		Type string `json:"Type"`
		Path string `json:"Path"`
	} `json:"ListingProviders"`
}

func sameURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}

// SetUpLiveTV adds the tuner (an HDHomeRun at baseURL) and the XMLTV guide
// unless they exist, then starts a guide refresh.
func (c *Client) SetUpLiveTV(ctx context.Context, baseURL string) (mediaserver.LiveTVState, error) {
	var st mediaserver.LiveTVState
	var opts liveTVOptions
	if err := c.get(ctx, "/System/Configuration/livetv", nil, &opts); err != nil {
		return st, err
	}
	epg := strings.TrimRight(baseURL, "/") + "/epg.xml"
	for _, h := range opts.TunerHosts {
		if strings.EqualFold(h.Type, "hdhomerun") && sameURL(h.URL, baseURL) {
			st.Tuner = true
		}
	}
	for _, p := range opts.ListingProviders {
		if strings.EqualFold(p.Type, "xmltv") && sameURL(p.Path, epg) {
			st.Guide = true
		}
	}
	if !st.Tuner {
		body := map[string]any{"Type": "hdhomerun", "Url": baseURL, "FriendlyName": "Flimmerkiste", "ImportFavoritesOnly": false, "AllowHWTranscoding": false}
		if err := c.do(ctx, http.MethodPost, "/LiveTv/TunerHosts", nil, body, nil); err != nil {
			return st, err
		}
		st.Tuner, st.TunerAdded = true, true
	}
	if !st.Guide {
		body := map[string]any{"Type": "xmltv", "Path": epg, "EnableAllTuners": true}
		q := url.Values{"validateListings": {"false"}, "validateLogin": {"false"}}
		if err := c.do(ctx, http.MethodPost, "/LiveTv/ListingProviders", q, body, nil); err != nil {
			return st, err
		}
		st.Guide, st.GuideAdded = true, true
	}
	_, err := c.refreshGuide(ctx)
	return st, err
}

// LiveTVStatus reports whether tuner and guide are set up.
func (c *Client) LiveTVStatus(ctx context.Context, baseURL string) (mediaserver.LiveTVState, error) {
	var st mediaserver.LiveTVState
	var opts liveTVOptions
	if err := c.get(ctx, "/System/Configuration/livetv", nil, &opts); err != nil {
		return st, err
	}
	epg := strings.TrimRight(baseURL, "/") + "/epg.xml"
	for _, h := range opts.TunerHosts {
		st.Tuner = st.Tuner || (strings.EqualFold(h.Type, "hdhomerun") && sameURL(h.URL, baseURL))
	}
	for _, p := range opts.ListingProviders {
		st.Guide = st.Guide || (strings.EqualFold(p.Type, "xmltv") && sameURL(p.Path, epg))
	}
	return st, nil
}

// refreshGuide starts the scheduled task that refreshes the guide.
func (c *Client) refreshGuide(ctx context.Context) (bool, error) {
	var tasks []struct {
		ID   string `json:"Id"`
		Key  string `json:"Key"`
		Name string `json:"Name"`
	}
	if err := c.get(ctx, "/ScheduledTasks", nil, &tasks); err != nil {
		return false, err
	}
	for _, t := range tasks {
		if t.Key == "RefreshGuide" || strings.EqualFold(t.Name, "Refresh Guide") {
			return true, c.do(ctx, http.MethodPost, "/ScheduledTasks/Running/"+url.PathEscape(t.ID), nil, nil, nil)
		}
	}
	return false, nil
}

// ReloadGuide refreshes the guide when our guide is set up (returns 1, else 0).
func (c *Client) ReloadGuide(ctx context.Context, deviceID, baseURL string) (int, error) {
	if baseURL != "" {
		if st, err := c.LiveTVStatus(ctx, baseURL); err != nil || !st.Guide {
			return 0, err
		}
	}
	ok, err := c.refreshGuide(ctx)
	if !ok || err != nil {
		return 0, err
	}
	return 1, nil
}
