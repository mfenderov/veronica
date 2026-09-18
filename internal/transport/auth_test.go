package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithBearerAuth(t *testing.T) {
	const token = "gateway-token"

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
		wantCalled    bool
	}{
		{name: "missing header", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", authorization: "Basic " + token, wantStatus: http.StatusUnauthorized},
		{name: "wrong token", authorization: "Bearer wrong-token", wantStatus: http.StatusUnauthorized},
		{name: "empty bearer value", authorization: "Bearer ", wantStatus: http.StatusUnauthorized},
		{name: "valid token", authorization: "Bearer " + token, wantStatus: http.StatusNoContent, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "http://gateway.test/", http.NoBody)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			res := httptest.NewRecorder()

			WithBearerAuth(next, token).ServeHTTP(res, req)

			if res.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, tt.wantStatus)
			}
			if called != tt.wantCalled {
				t.Fatalf("next called = %v, want %v", called, tt.wantCalled)
			}
			if tt.wantCalled {
				if got := res.Header().Get("WWW-Authenticate"); got != "" {
					t.Fatalf("WWW-Authenticate = %q for valid token, want empty", got)
				}
				return
			}
			if got := res.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q, want Bearer", got)
			}
		})
	}
}
