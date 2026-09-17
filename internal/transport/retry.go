package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"time"
)

// RetryConfig specifies configuration parameters for RetryRoundTripper.
type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

// DefaultRetryConfig returns sensible defaults for downstream HTTP retries.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		BackoffFactor:  2.0,
	}
}

// RetryRoundTripper wraps an http.RoundTripper with exponential backoff retries
// for transient network errors and server errors (429, 502, 503, 504).
type RetryRoundTripper struct {
	next   http.RoundTripper
	config RetryConfig
}

// NewRetryRoundTripper constructs a RetryRoundTripper with the provided config.
func NewRetryRoundTripper(next http.RoundTripper, cfg RetryConfig) *RetryRoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 2
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 1 * time.Second
	}
	if cfg.BackoffFactor <= 1.0 {
		cfg.BackoffFactor = 2.0
	}
	return &RetryRoundTripper{
		next:   next,
		config: cfg,
	}
}

// RoundTrip executes an HTTP request, retrying on transient errors according to config.
func (rt *RetryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	bufferRequestBody(req)

	var lastResp *http.Response
	var lastErr error

	for attempt := 0; attempt <= rt.config.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := rt.prepareRetry(req.Context(), attempt-1, req); err != nil {
				return nil, err
			}
		}

		resp, err := rt.next.RoundTrip(req)
		if err != nil {
			if !isRetryableError(err) {
				return nil, err
			}
			lastErr = err
			continue
		}

		if !isRetryableStatus(resp.StatusCode) {
			return resp, nil
		}

		drainAndClose(resp.Body)
		lastResp = resp
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return lastResp, nil
}

func (rt *RetryRoundTripper) prepareRetry(ctx context.Context, attempt int, req *http.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := computeBackoff(attempt, rt.config)
	if err := sleepWithContext(ctx, delay); err != nil {
		return err
	}
	return rewindBody(req)
}

func bufferRequestBody(req *http.Request) {
	if req.Body == nil || req.GetBody != nil {
		return
	}
	bodyBytes, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return
	}
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}
	req.Body, _ = req.GetBody()
}

func rewindBody(req *http.Request) error {
	if req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return err
	}
	req.Body = body
	return nil
}

func isRetryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isRetryableError(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func computeBackoff(attempt int, cfg RetryConfig) time.Duration {
	backoff := float64(cfg.InitialBackoff)
	for i := 0; i < attempt; i++ {
		backoff *= cfg.BackoffFactor
		if backoff > float64(cfg.MaxBackoff) {
			backoff = float64(cfg.MaxBackoff)
			break
		}
	}
	jitter := time.Duration(rand.Int64N(int64(cfg.InitialBackoff) / 2))
	return time.Duration(backoff) + jitter
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4096))
	_ = body.Close()
}
