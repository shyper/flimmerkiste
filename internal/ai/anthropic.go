package ai

import (
	"context"
	"errors"
	"fmt"

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
	return &anthropicProvider{client: anthropic.NewClient(option.WithAPIKey(key)), model: model}
}

func (p *anthropicProvider) Pick(ctx context.Context, req Request) ([]Pick, error) {
	resp, err := p.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
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
