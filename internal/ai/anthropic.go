package ai

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/shyper/flimmerkiste/internal/i18n"
)

const DefaultAnthropicModel = "claude-opus-5-5"

type anthropicProvider struct {
	client anthropic.Client
	model  string
}

func newAnthropic(key, model string) *anthropicProvider {
	return &anthropicProvider{client: anthropic.NewClient(option.WithAPIKey(key), option.WithHTTPClient(aiHTTP)), model: model}
}

func (p *anthropicProvider) Pick(ctx context.Context, req Request) ([]Pick, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	stream := p.client.Beta.Messages.NewStreaming(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt(req)}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(userPrompt(req))),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Format: anthropic.BetaJSONOutputFormatParam{Schema: pickSchema},
		},
		// If a request is declined by a safety classifier, let the API re-serve it
		// with a suitable fallback model instead of failing.
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	})
	defer stream.Close()
	var resp anthropic.BetaMessage
	complete := false
	for stream.Next() {
		event := stream.Current()
		if err := resp.Accumulate(event); err != nil {
			return nil, fmt.Errorf("Anthropic: %w", err)
		}
		if event.Type == "message_stop" {
			complete = true
			break
		}
	}
	err := stream.Err()
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case 401, 403:
				return nil, i18n.E("ai.unauthorized")
			case 429:
				return nil, i18n.E("ai.rateLimit")
			}
			return nil, fmt.Errorf("Anthropic: HTTP %d", apiErr.StatusCode)
		}
		return nil, fmt.Errorf("Anthropic: %w", err)
	}
	if !complete {
		return nil, fmt.Errorf("Anthropic: %w", io.ErrUnexpectedEOF)
	}
	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return nil, i18n.E("ai.refused")
	case anthropic.BetaStopReasonMaxTokens:
		return nil, i18n.E("ai.truncated")
	}
	var text string
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text += tb.Text
		}
	}
	return parsePicks(text, req)
}
