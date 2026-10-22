package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shyper/flimmerkiste/internal/store"
)

// bearer adds the token to every request of the MCP client.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mcpSession creates a token with the scopes through the web API and connects with it.
func mcpSession(t *testing.T, h http.Handler, ts *httptest.Server, scopes ...string) *mcp.ClientSession {
	t.Helper()
	var created struct {
		Token string `json:"token"`
	}
	if code := call(t, h, "POST", "/api/tokens", map[string]any{"name": "Claude", "scopes": scopes}, &created); code != http.StatusCreated {
		t.Fatalf("create token: %d", code)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{created.Token}}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// useTool calls a tool and decodes its JSON answer into out; it returns the error text of a failed call.
func useTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		return text
	}
	if out != nil {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			t.Fatalf("%s: %v: %s", name, err, text)
		}
	}
	return ""
}

// A token only offers the tools of its scopes.
func TestMCPScopes(t *testing.T) {
	_, h := transferWorld(t, "a")
	ts := httptest.NewServer(h)
	defer ts.Close()

	read := toolNames(t, mcpSession(t, h, ts, ScopeRead))
	if !slices.Contains(read, "list_channels") || !slices.Contains(read, "get_guide") {
		t.Errorf("read token lacks read tools: %v", read)
	}
	for _, name := range []string{"search_library", "create_channel", "delete_channel"} {
		if slices.Contains(read, name) {
			t.Errorf("read token offers %s", name)
		}
	}
	all := toolNames(t, mcpSession(t, h, ts, ScopeRead, ScopeLibrary, ScopeWrite))
	if !slices.Contains(all, "search_library") || !slices.Contains(all, "create_channel") || slices.Contains(all, "delete_channel") {
		t.Errorf("read+library+write: %v", all)
	}
}

// Through MCP a channel is created from library titles, changed in part and deleted.
func TestMCPChannelLifecycle(t *testing.T) {
	srv, h := transferWorld(t, "a")
	ts := httptest.NewServer(h)
	defer ts.Close()
	cs := mcpSession(t, h, ts, Scopes...)

	var found struct {
		Movies []mediaView `json:"movies"`
	}
	useTool(t, cs, "search_library", map[string]any{"query": "matrix"}, &found)
	if len(found.Movies) != 1 || found.Movies[0].Key != "a1" {
		t.Fatalf("search: %+v", found)
	}
	var title store.Media
	if msg := useTool(t, cs, "get_title", map[string]any{"key": "a1"}, &title); msg != "" || title.Year != 1999 {
		t.Fatalf("get_title: %q %+v", msg, title)
	}

	var created channelView
	msg := useTool(t, cs, "create_channel", map[string]any{"name": "Kult", "kind": "manual", "config": map[string]any{
		"source": map[string]any{"type": "manual", "items": []map[string]any{{"key": "a1", "type": "movie", "title": "Matrix"}}}}}, &created)
	if msg != "" || created.ID == 0 {
		t.Fatalf("create: %q", msg)
	}
	c, _ := srv.store.GetChannel(context.Background(), created.ID)
	if !strings.Contains(string(c.Config), `"order":"fixed"`) || !strings.Contains(string(c.Config), `"transcode":"default"`) {
		t.Errorf("editor defaults missing: %s", c.Config)
	}

	// only the name and one config field change
	msg = useTool(t, cs, "update_channel", map[string]any{"id": created.ID, "name": "Kultkino", "config": map[string]any{"transcode": "full"}}, nil)
	if msg != "" {
		t.Fatalf("update: %s", msg)
	}
	c, _ = srv.store.GetChannel(context.Background(), created.ID)
	if c.Name != "Kultkino" || c.Number != created.Number || !strings.Contains(string(c.Config), `"transcode":"full"`) || !strings.Contains(string(c.Config), `"key":"a1"`) {
		t.Errorf("after update: %s %d %s", c.Name, c.Number, c.Config)
	}

	// errors come back as tool errors in the installation's language
	if msg := useTool(t, cs, "get_channel", map[string]any{"id": 999}, nil); msg != "Sender nicht gefunden" {
		t.Errorf("unknown channel: %q", msg)
	}
	if msg := useTool(t, cs, "create_channel", map[string]any{"name": " ", "kind": "smart", "config": map[string]any{}}, nil); msg == "" {
		t.Error("a channel without a name was created")
	}

	if msg := useTool(t, cs, "delete_channel", map[string]any{"id": created.ID}, nil); msg != "" {
		t.Fatalf("delete: %s", msg)
	}
	if _, err := srv.store.GetChannel(context.Background(), created.ID); err == nil {
		t.Error("channel still there")
	}
}

// The token acts with the current rights of its user: demoted, only viewer tools remain;
// a revoked or unknown token gets no answer.
func TestMCPTokenUser(t *testing.T) {
	srv, h := transferWorld(t, "a")
	ts := httptest.NewServer(h)
	defer ts.Close()
	ctx := context.Background()
	cs := mcpSession(t, h, ts, Scopes...)

	users, _ := srv.store.ListUsers(ctx)
	srv.store.SetUserRole(ctx, users[0].ID, "viewer")
	names := toolNames(t, cs)
	if !slices.Contains(names, "list_channels") || slices.Contains(names, "create_channel") || slices.Contains(names, "search_library") {
		t.Errorf("as viewer: %v", names)
	}
	srv.store.SetUserRole(ctx, users[0].ID, "admin")

	var list struct {
		Tokens []store.APIToken `json:"tokens"`
	}
	call(t, h, "GET", "/api/tokens", nil, &list)
	if len(list.Tokens) != 1 || list.Tokens[0].Username != "owner" || len(list.Tokens[0].Scopes) != len(Scopes) {
		t.Fatalf("tokens: %+v", list.Tokens)
	}
	if code := call(t, h, "DELETE", "/api/tokens/"+strconv.FormatInt(list.Tokens[0].ID, 10), nil, nil); code != http.StatusOK {
		t.Fatalf("revoke: %d", code)
	}
	if _, err := cs.ListTools(ctx, nil); err == nil {
		t.Error("a revoked token still works")
	}

	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer fk_nonsense")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown token: %d", res.StatusCode)
	}
}

func TestCreateTokenChecksScopes(t *testing.T) {
	_, h := transferWorld(t, "a")
	for _, scopes := range [][]string{nil, {"admin"}, {ScopeRead, "everything"}} {
		if code := call(t, h, "POST", "/api/tokens", map[string]any{"name": "x", "scopes": scopes}, nil); code != http.StatusBadRequest {
			t.Errorf("scopes %v: %d", scopes, code)
		}
	}
	if code := call(t, h, "POST", "/api/tokens", map[string]any{"name": "", "scopes": []string{ScopeRead}}, nil); code != http.StatusBadRequest {
		t.Errorf("without a name: %d", code)
	}
}

// The Claude and ChatGPT apps cannot send a header: the token works as part of the address too.
func TestMCPTokenInPath(t *testing.T) {
	_, h := transferWorld(t, "a")
	ts := httptest.NewServer(h)
	defer ts.Close()
	var created struct {
		Token string `json:"token"`
	}
	call(t, h, "POST", "/api/tokens", map[string]any{"name": "Claude", "scopes": []string{ScopeRead}}, &created)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp/" + created.Token, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()
	if names := toolNames(t, cs); !slices.Contains(names, "list_channels") || slices.Contains(names, "create_channel") {
		t.Errorf("tools: %v", names)
	}

	for path, want := range map[string]int{
		"/mcp/fk_nonsense": http.StatusUnauthorized,
		"/mcp":             http.StatusUnauthorized,
	} {
		res, err := http.Post(ts.URL+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, res.StatusCode, want)
		}
	}
}
