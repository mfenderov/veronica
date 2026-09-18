package transport

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// WithBearerAuth protects an HTTP handler with a bearer token.
func WithBearerAuth(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		valid := len(parts) == 2 &&
			strings.EqualFold(parts[0], "Bearer") &&
			parts[1] != "" &&
			subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) == 1
		if !valid {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
