// Package jellyfin talks to Jellyfin and Emby servers. Both grew from the same
// code base and share most of their API; the differences (auth headers, a few
// paths) depend on the Flavor.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

// Flavor tells Jellyfin and Emby apart.
type Flavor string

const (
	Jellyfin Flavor = "jellyfin"
	Emby     Flavor = "emby"
)

const (
	clientName = "Flimmerkiste"
	// ticksPerMs: the API counts time in 100 ns ticks.
	ticksPerMs = 10_000
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

// Client is a connection to one server.
type Client struct {
	Flavor   Flavor
	BaseURL  string
	Token    string // API key or session token
	DeviceID string // identifies this installation to the server
	UserID   string // whose watched state and libraries count (the owner)
}

// StatusError is an HTTP error answer.
type StatusError struct {
	Code int
	URL  string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d from %s", "media server", e.Code, e.URL)
}

// Is lets errors.Is(err, mediaserver.ErrNotFound) match a 404.
func (e *StatusError) Is(target error) bool {
	return target == mediaserver.ErrNotFound && e.Code == http.StatusNotFound
}

func (c *Client) base() string { return strings.TrimRight(c.BaseURL, "/") }

// authHeader identifies the client (and the user with a token).
func (c *Client) setHeaders(req *http.Request) {
	device := c.DeviceID
	if device == "" {
		device = "flimmerkiste"
	}
	v := fmt.Sprintf(`MediaBrowser Client="%s", Device="%s", DeviceId="%s", Version="1.0"`, clientName, clientName, device)
	if c.Token != "" {
		v += fmt.Sprintf(`, Token="%s"`, c.Token)
	}
	if c.Flavor == Emby {
		req.Header.Set("X-Emby-Authorization", strings.Replace(v, "MediaBrowser ", "Emby ", 1))
		if c.Token != "" {
			req.Header.Set("X-Emby-Token", c.Token)
		}
	} else {
		req.Header.Set("Authorization", v)
	}
	req.Header.Set("Accept", "application/json")
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.base() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return &StatusError{Code: resp.StatusCode, URL: mediaserver.Redact(req.URL.Redacted())}
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// PublicInfo is what a server tells without sign-in.
type PublicInfo struct {
	ID          string `json:"Id"`
	ServerName  string `json:"ServerName"`
	Version     string `json:"Version"`
	ProductName string `json:"ProductName"`
	// StartupWizardCompleted is false on a fresh server.
	StartupWizardCompleted bool `json:"StartupWizardCompleted"`
}

// Public returns the public information of the server at baseURL; it also
// tells Jellyfin from Emby.
func Public(ctx context.Context, baseURL string) (*PublicInfo, Flavor, error) {
	c := &Client{BaseURL: baseURL}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var info PublicInfo
	if err := c.get(ctx, "/System/Info/Public", nil, &info); err != nil {
		return nil, "", err
	}
	flavor := Emby
	if strings.Contains(strings.ToLower(info.ProductName), "jellyfin") {
		flavor = Jellyfin
	}
	return &info, flavor, nil
}

// Session is a successful sign-in.
type Session struct {
	UserID   string
	UserName string
	Admin    bool
	Token    string
	ServerID string
}

// Login signs a user in with name and password.
func Login(ctx context.Context, flavor Flavor, baseURL, deviceID, user, password string) (*Session, error) {
	c := &Client{Flavor: flavor, BaseURL: baseURL, DeviceID: deviceID}
	var r struct {
		User struct {
			ID     string `json:"Id"`
			Name   string `json:"Name"`
			Policy struct {
				IsAdministrator bool `json:"IsAdministrator"`
			} `json:"Policy"`
		} `json:"User"`
		AccessToken string `json:"AccessToken"`
		ServerID    string `json:"ServerId"`
	}
	if err := c.do(ctx, http.MethodPost, "/Users/AuthenticateByName", nil, map[string]string{"Username": user, "Pw": password}, &r); err != nil {
		return nil, err
	}
	return &Session{UserID: r.User.ID, UserName: r.User.Name, Admin: r.User.Policy.IsAdministrator, Token: r.AccessToken, ServerID: r.ServerID}, nil
}

// Logout ends the session of the client's token.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/Sessions/Logout", nil, nil, nil)
}

// CreateAPIKey makes an API key for this application (admins only) and
// returns it; an existing key of the same name is reused.
func (c *Client) CreateAPIKey(ctx context.Context, app string) (string, error) {
	find := func() (string, error) {
		var r struct {
			Items []struct {
				AccessToken string `json:"AccessToken"`
				AppName     string `json:"AppName"`
			} `json:"Items"`
		}
		if err := c.get(ctx, "/Auth/Keys", nil, &r); err != nil {
			return "", err
		}
		for _, k := range r.Items {
			if k.AppName == app {
				return k.AccessToken, nil
			}
		}
		return "", nil
	}
	if k, err := find(); err != nil || k != "" {
		return k, err
	}
	param := "app"
	if c.Flavor == Emby {
		param = "App"
	}
	if err := c.do(ctx, http.MethodPost, "/Auth/Keys", url.Values{param: {app}}, nil, nil); err != nil {
		return "", err
	}
	k, err := find()
	if err == nil && k == "" {
		err = fmt.Errorf("the server did not list the new API key")
	}
	return k, err
}

// User is an account of the server.
type User struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	Admin bool   `json:"-"`
}

// Users lists the accounts (admins only).
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var r []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.get(ctx, "/Users", nil, &r); err != nil {
		return nil, err
	}
	out := make([]User, len(r))
	for i, u := range r {
		out[i] = User{ID: u.ID, Name: u.Name, Admin: u.Policy.IsAdministrator}
	}
	return out, nil
}
