package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/shyper/flimmerkiste/internal/i18n"
)

// openAI talks to any OpenAI-compatible chat completions endpoint
// (OpenAI, Ollama, LM Studio, OpenRouter, …).
type openAI struct {
	baseURL string
	key     string
	model   string
}

func (p *openAI) Pick(ctx context.Context, req Request) ([]Pick, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt(req)},
			{"role": "user", "content": userPrompt(req)},
		},
		"response_format": map[string]string{"type": "json_object"},
		"stream":          true,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
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
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		text, err := readOpenAIStream(resp.Body)
		if err != nil {
			return nil, err
		}
		return parsePicks(text, req)
	}
	// Some compatible servers ignore stream=true and still return a JSON message.
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

func readOpenAIStream(r io.Reader) (string, error) {
	const maxBytes = 4 << 20
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), maxBytes)
	var text, data strings.Builder
	finished := false
	consume := func() (bool, error) {
		payload := data.String()
		data.Reset()
		if payload == "" {
			return false, nil
		}
		if strings.TrimSpace(payload) == "[DONE]" {
			return true, nil
		}
		var chunk struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content string `json:"content"`
					Refusal string `json:"refusal"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return false, err
		}
		if chunk.Error != nil {
			return false, fmt.Errorf("AI stream: %s", excerpt(chunk.Error.Message, 300))
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Refusal != "" || choice.FinishReason == "content_filter" {
				return false, i18n.E("ai.refused")
			}
			if choice.FinishReason == "length" {
				return false, i18n.E("ai.truncated")
			}
			if text.Len()+len(choice.Delta.Content) > maxBytes {
				return false, i18n.E("ai.truncated")
			}
			text.WriteString(choice.Delta.Content)
			if choice.FinishReason == "stop" {
				finished = true
			}
		}
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, err := consume()
			if err != nil || done {
				return text.String(), err
			}
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		if field == "data" {
			if data.Len()+len(value)+1 > maxBytes {
				return "", i18n.E("ai.truncated")
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	done, err := consume()
	if err != nil {
		return "", err
	}
	if !done && !finished {
		return "", io.ErrUnexpectedEOF
	}
	return text.String(), nil
}
