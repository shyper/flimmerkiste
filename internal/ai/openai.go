package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/shyper/flimmerkiste/internal/i18n"
)

// openAI talks to any OpenAI-compatible chat completions endpoint
// (OpenAI, Ollama, LM Studio, OpenRouter, …).
type openAI struct {
	baseURL string
	key     string
	model   string
}

var aiHTTP = &http.Client{Timeout: 5 * time.Minute}

func (p *openAI) Pick(ctx context.Context, req Request) ([]Pick, error) {
	body, _ := json.Marshal(map[string]any{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt(req)},
			{"role": "user", "content": userPrompt(req)},
		},
		"response_format": map[string]string{"type": "json_object"},
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := aiHTTP.Do(httpReq)
	if err != nil {
		return nil, i18n.E("ai.unreachable", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, i18n.E("ai.httpError", resp.StatusCode, string(msg))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, i18n.E("ai.empty")
	}
	return parsePicks(out.Choices[0].Message.Content, req)
}
