package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const authTokenPrefix = "Bearer "

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		token, found := strings.CutPrefix(h, authTokenPrefix)
		token = strings.TrimSpace(token)
		if !found || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.authToken)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
