package auth

import (
	"testing"
)

func TestBrowserCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		goos    string
		wantBin string
	}{
		{goos: "darwin", wantBin: "open"},
		{goos: "windows", wantBin: "rundll32"},
		{goos: "linux", wantBin: "xdg-open"},
	}

	for _, tt := range tests {
		bin, args := browserCommand(tt.goos, "https://example.com")
		if bin != tt.wantBin {
			t.Errorf("browserCommand(%s) bin = %s, want %s", tt.goos, bin, tt.wantBin)
		}
		if len(args) == 0 {
			t.Errorf("browserCommand(%s) args empty", tt.goos)
		}
	}
}
