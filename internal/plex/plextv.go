// Package plex talks to plex.tv (account login) and to a Plex Media Server.
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const Product = "Flimmerkiste"

// httpClient follows redirects only on the same host: the X-Plex-Token header
// would otherwise go along to wherever a server points.
var httpClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: sameHostRedirect}

func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Hostname() != via[0].URL.Hostname() {
		return http.ErrUseLastResponse
	}
	return nil
}

func setHeaders(req *http.Request, clientID, token string) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", Product)
	req.Header.Set("X-Plex-Version", "1.0")
	req.Header.Set("X-Plex-Client-Identifier", clientID)
	req.Header.Set("X-Plex-Device-Name", Product)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
}

func doJSON(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return &StatusError{Code: resp.StatusCode, URL: req.URL.Redacted()}
	}
	if out == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16)) // so the connection is reused
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type StatusError struct {
	Code int
	URL  string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("plex: HTTP %d from %s", e.Code, e.URL)
}

// Pin is a plex.tv PIN used for the browser based OAuth flow.
type Pin struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	AuthToken string `json:"authToken"`
}

func CreatePin(ctx context.Context, clientID string) (*Pin, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://plex.tv/api/v2/pins?strong=true", nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req, clientID, "")
	var p Pin
	return &p, doJSON(req, &p)
}

func CheckPin(ctx context.Context, clientID string, id int64) (*Pin, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://plex.tv/api/v2/pins/%d", id), nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req, clientID, "")
	var p Pin
	return &p, doJSON(req, &p)
}

// AuthURL is where the user's browser is sent to approve the PIN.
func AuthURL(clientID, code, forward string) string {
	v := url.Values{}
	v.Set("clientID", clientID)
	v.Set("code", code)
	v.Set("context[device][product]", Product)
	if forward != "" {
		v.Set("forwardUrl", forward)
	}
	return "https://app.plex.tv/auth#?" + v.Encode()
}

type Account struct {
	ID       int64  `json:"id"`
	UUID     string `json:"uuid"`
	Username string `json:"username"`
	Title    string `json:"title"`
	Email    string `json:"email"`
	Thumb    string `json:"thumb"`
}

func GetAccount(ctx context.Context, clientID, token string) (*Account, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://plex.tv/api/v2/user", nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req, clientID, token)
	var a Account
	return &a, doJSON(req, &a)
}

type Connection struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	URI      string `json:"uri"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
}

type Resource struct {
	Name             string       `json:"name"`
	Product          string       `json:"product"`
	ClientIdentifier string       `json:"clientIdentifier"`
	Provides         string       `json:"provides"`
	Owned            bool         `json:"owned"`
	AccessToken      string       `json:"accessToken"`
	Connections      []Connection `json:"connections"`
}

// Servers lists the Plex Media Servers an account can access (owned and shared).
func Servers(ctx context.Context, clientID, token string) ([]Resource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://plex.tv/api/v2/resources?includeHttps=1&includeRelay=0&includeIPv6=0", nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req, clientID, token)
	var all []Resource
	if err := doJSON(req, &all); err != nil {
		return nil, err
	}
	var out []Resource
	for _, r := range all {
		if containsWord(r.Provides, "server") {
			out = append(out, r)
		}
	}
	return out, nil
}

func containsWord(list, word string) bool {
	start := 0
	for i := 0; i <= len(list); i++ {
		if i == len(list) || list[i] == ',' {
			if list[start:i] == word {
				return true
			}
			start = i + 1
		}
	}
	return false
}
