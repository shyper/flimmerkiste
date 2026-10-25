package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RequestTimeout bounds even a stream that keeps sending heartbeats forever.
const RequestTimeout = 15 * time.Minute

const streamIdleTimeout = 4 * time.Minute

var aiHTTP = &http.Client{
	Timeout:   RequestTimeout,
	Transport: idleTransport{base: http.DefaultTransport, timeout: streamIdleTimeout},
}

// idleTransport times out silence, including the wait for response headers.
// Any received bytes (also SSE heartbeats and reasoning deltas) reset the clock.
type idleTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (t idleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	timer := time.AfterFunc(t.timeout, func() {
		cancel(fmt.Errorf("AI stream inactive for %s: %w", t.timeout, context.DeadlineExceeded))
	})
	resp, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		timer.Stop()
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		cancel(nil)
		return nil, err
	}
	timer.Reset(t.timeout)
	resp.Body = &idleBody{ReadCloser: resp.Body, ctx: ctx, timer: timer, cancel: cancel, timeout: t.timeout}
	return resp, nil
}

type idleBody struct {
	io.ReadCloser
	ctx     context.Context
	timer   *time.Timer
	cancel  context.CancelCauseFunc
	timeout time.Duration
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.timeout)
	}
	if err != nil && context.Cause(b.ctx) != nil {
		err = context.Cause(b.ctx)
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	b.cancel(nil)
	return b.ReadCloser.Close()
}
