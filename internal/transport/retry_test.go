package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryRoundTripper_SuccessNoRetry(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	rt := NewRetryRoundTripper(http.DefaultTransport, RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		BackoffFactor:  2.0,
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(1), attempts.Load())
}

func TestRetryRoundTripper_TransientErrorRetried(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("502 Bad Gateway"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"recovered"}`))
	}))
	defer server.Close()

	rt := NewRetryRoundTripper(http.DefaultTransport, RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		BackoffFactor:  2.0,
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, bytes.NewReader([]byte("payload")))
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "recovered")
	assert.Equal(t, int32(2), attempts.Load())
}

func TestRetryRoundTripper_MaxRetriesExceeded(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("503 Service Unavailable"))
	}))
	defer server.Close()

	rt := NewRetryRoundTripper(http.DefaultTransport, RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		BackoffFactor:  2.0,
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, int32(3), attempts.Load()) // 1 initial + 2 retries
}

func TestRetryRoundTripper_NonRetryableStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(status)
		}))

		rt := NewRetryRoundTripper(http.DefaultTransport, RetryConfig{
			MaxRetries:     2,
			InitialBackoff: 5 * time.Millisecond,
		})

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
		require.NoError(t, err)

		resp, err := rt.RoundTrip(req)
		require.NoError(t, err)
		resp.Body.Close()
		server.Close()

		assert.Equal(t, status, resp.StatusCode)
		assert.Equal(t, int32(1), attempts.Load())
	}
}

func TestRetryRoundTripper_ContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		cancel() // Cancel context on first failure
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer server.Close()

	rt := NewRetryRoundTripper(http.DefaultTransport, RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.Equal(t, int32(1), attempts.Load())
}

func TestRetryRoundTripper_BodyReplay(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	var receivedBodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBodies = append(receivedBodies, string(body))
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rt := NewRetryRoundTripper(http.DefaultTransport, RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 5 * time.Millisecond,
	})

	payload := "important jsonrpc payload"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, bytes.NewReader([]byte(payload)))
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, int32(2), attempts.Load())
	require.Len(t, receivedBodies, 2)
	assert.Equal(t, payload, receivedBodies[0])
	assert.Equal(t, payload, receivedBodies[1])
}

func TestRetryRoundTripper_BufferRequestBodyNoGetBody(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost", io.NopCloser(bytes.NewReader([]byte("test"))))
	require.NoError(t, err)
	req.GetBody = nil

	bufferRequestBody(req)
	require.NotNil(t, req.GetBody)
	b, err := req.GetBody()
	require.NoError(t, err)
	data, _ := io.ReadAll(b)
	assert.Equal(t, "test", string(data))
}

func TestRetryRoundTripper_IsRetryableError(t *testing.T) {
	t.Parallel()

	assert.False(t, isRetryableError(context.Canceled))
	assert.False(t, isRetryableError(context.DeadlineExceeded))
	assert.True(t, isRetryableError(io.EOF))
	assert.True(t, isRetryableError(errors.New("connection reset by peer")))
}

