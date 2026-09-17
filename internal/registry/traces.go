package registry

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
)

// RingBufferTraceRecorder implements domain.TraceRecorder using a fixed-capacity circular buffer.
type RingBufferTraceRecorder struct {
	mu       sync.RWMutex
	capacity int
	entries  []domain.ToolTrace
	start    int
}

// NewRingBufferTraceRecorder creates a RingBufferTraceRecorder with the specified capacity.
func NewRingBufferTraceRecorder(capacity int) *RingBufferTraceRecorder {
	if capacity <= 0 {
		capacity = 100
	}
	return &RingBufferTraceRecorder{
		capacity: capacity,
		entries:  make([]domain.ToolTrace, 0, capacity),
	}
}

// Record appends a new ToolTrace to the ring buffer, overwriting the oldest entry when full.
func (r *RingBufferTraceRecorder) Record(trace domain.ToolTrace) {
	if trace.ID == "" {
		trace.ID = generateTraceID()
	}
	if trace.Timestamp.IsZero() {
		trace.Timestamp = time.Now()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.entries) < r.capacity {
		r.entries = append(r.entries, trace)
		return
	}

	r.entries[r.start] = trace
	r.start = (r.start + 1) % r.capacity
}

// Recent returns the most recent recorded traces up to limit, ordered newest-first.
func (r *RingBufferTraceRecorder) Recent(limit int) []domain.ToolTrace {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := len(r.entries)
	if count == 0 || limit <= 0 {
		return nil
	}
	if limit > count {
		limit = count
	}

	out := make([]domain.ToolTrace, limit)
	for i := 0; i < limit; i++ {
		// Newest entry index in circular buffer:
		// When not full: count - 1 - i
		// When full: (start - 1 - i + capacity) % capacity
		idx := r.itemIndex(count, i)
		out[i] = r.entries[idx]
	}
	return out
}

func (r *RingBufferTraceRecorder) itemIndex(count, offset int) int {
	if count < r.capacity {
		return count - 1 - offset
	}
	return (r.start - 1 - offset + r.capacity) % r.capacity
}

func generateTraceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
