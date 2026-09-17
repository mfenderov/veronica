package registry

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRingBufferTraceRecorder_Capacity(t *testing.T) {
	t.Parallel()

	recorder := NewRingBufferTraceRecorder(10)
	for i := 0; i < 25; i++ {
		recorder.Record(domain.ToolTrace{
			ID:         fmt.Sprintf("trace-%d", i),
			Timestamp:  time.Now(),
			ModuleName: "test-mod",
			ToolName:   fmt.Sprintf("tool-%d", i),
			Duration:   time.Duration(i) * time.Millisecond,
		})
	}

	recent := recorder.Recent(20)
	require.Len(t, recent, 10)
	// Newest should be trace-24
	assert.Equal(t, "trace-24", recent[0].ID)
	// Oldest retained should be trace-15
	assert.Equal(t, "trace-15", recent[9].ID)
}

func TestRingBufferTraceRecorder_Limit(t *testing.T) {
	t.Parallel()

	recorder := NewRingBufferTraceRecorder(50)
	for i := 0; i < 10; i++ {
		recorder.Record(domain.ToolTrace{
			ID:        fmt.Sprintf("trace-%d", i),
			Timestamp: time.Now(),
		})
	}

	recent := recorder.Recent(3)
	require.Len(t, recent, 3)
	assert.Equal(t, "trace-9", recent[0].ID)
	assert.Equal(t, "trace-8", recent[1].ID)
	assert.Equal(t, "trace-7", recent[2].ID)
}

func TestRingBufferTraceRecorder_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	recorder := NewRingBufferTraceRecorder(100)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				recorder.Record(domain.ToolTrace{
					ID:         fmt.Sprintf("trace-%d-%d", id, j),
					Timestamp:  time.Now(),
					ModuleName: "mod",
					ToolName:   "tool",
					Duration:   time.Millisecond,
				})
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = recorder.Recent(10)
			}
		}()
	}

	wg.Wait()
	recent := recorder.Recent(100)
	assert.Len(t, recent, 100)
}

type traceCapturingClient struct {
	result domain.ToolResult
	err    error
}

func (c *traceCapturingClient) Start(ctx context.Context) error { return nil }
func (c *traceCapturingClient) Stop(ctx context.Context) error  { return nil }
func (c *traceCapturingClient) Status() domain.ModuleStatus     { return domain.StatusActive }
func (c *traceCapturingClient) ListTools(ctx context.Context) ([]domain.Tool, error) {
	return []domain.Tool{{Name: "echo"}}, nil
}
func (c *traceCapturingClient) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	return c.result, c.err
}

func TestRegistry_CallTool_RecordsTrace(t *testing.T) {
	t.Parallel()

	reg := New()
	mod := domain.NewModule(domain.ModuleConfig{
		Name:      "testmod",
		Transport: domain.TransportStdio,
		Command:   "echo",
	})
	cli := &traceCapturingClient{
		result: domain.ToolResult{Content: []domain.ToolContent{{Text: "hello"}}},
	}

	err := reg.Register(mod, cli)
	require.NoError(t, err)

	res, err := reg.CallTool(t.Context(), domain.ToolCall{
		ToolName: "testmod_echo",
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)

	traces := reg.RecentTraces(10)
	require.Len(t, traces, 1)
	assert.Equal(t, "testmod", traces[0].ModuleName)
	assert.Equal(t, "echo", traces[0].ToolName)
	assert.False(t, traces[0].IsError)
	assert.NotEmpty(t, traces[0].ID)
}
