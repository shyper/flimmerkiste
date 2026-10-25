package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const streamedPicks = `{"picks":[{"id":"1","reason":"Gemütlich."}]}`

func completionChunk(text, finish string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"index": 0, "delta": map[string]string{"content": text}, "finish_reason": finish,
	}}})
	return "data: " + string(b) + "\n\n"
}

func TestOpenAIStreamFraming(t *testing.T) {
	for _, tc := range []struct {
		name, stream string
		wantErr      bool
	}{
		{"fragments and heartbeats", ": keepalive\n\n" + completionChunk(streamedPicks[:20], "") +
			"data: {\"choices\":[]}\n\n" + completionChunk(streamedPicks[20:], "stop") + "data: [DONE]\n\n", false},
		{"CRLF", strings.ReplaceAll(completionChunk(streamedPicks, "stop"), "\n", "\r\n"), false},
		{"multiline data", "event: message\ndata: {\ndata: \"choices\":[{\"delta\":{\"content\":" +
			fmt.Sprintf("%q", streamedPicks) + "},\"finish_reason\":\"stop\"}]}\n\n", false},
		{"done at EOF", completionChunk(streamedPicks, "") + "data: [DONE]", false},
		{"missing completion", completionChunk(streamedPicks, ""), true},
		{"incomplete JSON event", "data: {\n\n", true},
		{"provider error", completionChunk(streamedPicks, "") + "data: {\"error\":{\"message\":\"overloaded\"}}\n\n", true},
		{"token limit", completionChunk(streamedPicks, "length") + "data: [DONE]\n\n", true},
		{"refused", completionChunk(streamedPicks, "content_filter") + "data: [DONE]\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exercise arbitrary network boundaries, including within UTF-8 text.
			got, err := readOpenAIStream(iotest.OneByteReader(strings.NewReader(tc.stream)))
			if (err != nil) != tc.wantErr || (!tc.wantErr && got != streamedPicks) {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestOpenAIPickStreamingAndJSONCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		wantErr                 bool
	}{
		{"stream", "text/event-stream", completionChunk(streamedPicks, "stop") + "data: [DONE]\n\n", 200, false},
		{"JSON server", "application/json", `{"choices":[{"message":{"content":` + fmt.Sprintf("%q", streamedPicks) + `}}]}`, 200, false},
		{"overloaded", "application/json", `{"error":{"message":"busy"}}`, 503, true},
		{"disconnected", "text/event-stream", completionChunk(streamedPicks, ""), 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Stream bool `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Stream || r.URL.Path != "/chat/completions" {
					t.Errorf("expected streaming completion request, got %+v, %v, %s", body, err, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Accept") != "text/event-stream" {
					t.Error("missing authentication or streaming headers")
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			p := &openAI{baseURL: srv.URL, key: "test-key", model: "test-model"}
			picks, err := p.Pick(context.Background(), Request{Candidates: []Candidate{{ID: "1"}}})
			if (err != nil) != tc.wantErr || (!tc.wantErr && (len(picks) != 1 || picks[0].Reason != "Gemütlich.")) {
				t.Fatalf("picks %+v, %v", picks, err)
			}
		})
	}
}

func anthropicEvents(stop string, complete bool) string {
	var out strings.Builder
	event := func(kind string, fields map[string]any) {
		fields["type"] = kind
		b, _ := json.Marshal(fields)
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", kind, b)
	}
	event("message_start", map[string]any{"message": map[string]any{
		"id": "msg_test", "type": "message", "role": "assistant", "model": "test-model",
		"content": []any{}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0},
	}})
	event("content_block_start", map[string]any{"index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
	for _, text := range []string{streamedPicks[:20], streamedPicks[20:]} {
		event("ping", map[string]any{})
		event("content_block_delta", map[string]any{"index": 0, "delta": map[string]string{"type": "text_delta", "text": text}})
	}
	event("content_block_stop", map[string]any{"index": 0})
	event("message_delta", map[string]any{"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 20}})
	if complete {
		event("message_stop", map[string]any{})
	}
	return out.String()
}

func TestAnthropicPickStreaming(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"complete", anthropicEvents("end_turn", true), 200, false},
		{"disconnected", anthropicEvents("end_turn", false), 200, true},
		{"refusal", anthropicEvents("refusal", true), 200, true},
		{"truncated", anthropicEvents("max_tokens", true), 200, true},
		{"stream error", "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n", 200, true},
		{"rate limit", `{"type":"error","error":{"type":"rate_limit_error","message":"busy"}}`, 429, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Stream       bool            `json:"stream"`
					OutputConfig json.RawMessage `json:"output_config"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Stream || len(body.OutputConfig) == 0 {
					t.Errorf("expected streaming request with output schema, got %+v, %v", body, err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			p := newAnthropic("test-key", "test-model")
			p.client = anthropic.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(srv.URL), option.WithHTTPClient(aiHTTP), option.WithMaxRetries(0))
			picks, err := p.Pick(context.Background(), Request{Candidates: []Candidate{{ID: "1"}}})
			if (err != nil) != tc.wantErr || (!tc.wantErr && (len(picks) != 1 || picks[0].Reason != "Gemütlich.")) {
				t.Fatalf("picks %+v, %v", picks, err)
			}
		})
	}
}

func TestIdleTransportTimesOutSilence(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(fmt.Sprint(headers), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if headers {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer srv.Close()
			client := &http.Client{Transport: idleTransport{base: http.DefaultTransport, timeout: 50 * time.Millisecond}, Timeout: 2 * time.Second}
			resp, err := client.Get(srv.URL)
			if err == nil {
				defer resp.Body.Close()
				_, err = io.ReadAll(resp.Body)
			}
			if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "inactive") {
				t.Fatalf("expected inactivity timeout, got %v", err)
			}
		})
	}
}

func TestIdleTransportResetsOnHeartbeats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 12 {
			io.WriteString(w, ": ping\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()
	client := &http.Client{Transport: idleTransport{base: http.DefaultTransport, timeout: 250 * time.Millisecond}, Timeout: 3 * time.Second}
	for _, cancelEarly := range []bool{false, true} {
		ctx := context.Background()
		cancel := func() {}
		if cancelEarly {
			ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		cancel()
		if cancelEarly {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("parent deadline was ignored: %v", err)
			}
		} else if err != nil {
			t.Fatalf("active stream failed: %v", err)
		}
	}
}
