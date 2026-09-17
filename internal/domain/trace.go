package domain

import "time"

// ToolTrace represents a single recorded execution of a downstream or meta tool.
type ToolTrace struct {
	ID         string        `json:"id"`
	Timestamp  time.Time     `json:"timestamp"`
	ModuleName string        `json:"module_name"`
	ToolName   string        `json:"tool_name"`
	Duration   time.Duration `json:"duration"`
	IsError    bool          `json:"is_error"`
	ErrorMsg   string        `json:"error_msg,omitempty"`
}

// TraceRecorder records and retrieves recent tool execution traces.
type TraceRecorder interface {
	Record(trace ToolTrace)
	Recent(limit int) []ToolTrace
}
