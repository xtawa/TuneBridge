package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

func ValidBasic(r *http.Request, expectedUsername, expectedPassword string) bool {
	username, password, ok := r.BasicAuth()
	providedUser := sha256.Sum256([]byte(username))
	expectedUser := sha256.Sum256([]byte(expectedUsername))
	providedPasswordHash := sha256.Sum256([]byte(password))
	expectedPasswordHash := sha256.Sum256([]byte(expectedPassword))
	userEqual := subtle.ConstantTimeCompare(providedUser[:], expectedUser[:])
	passwordEqual := subtle.ConstantTimeCompare(providedPasswordHash[:], expectedPasswordHash[:])
	return ok && userEqual == 1 && passwordEqual == 1
}

func RequireBasic(next http.Handler, username, password, realm string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ValidBasic(r, username, password) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
	})
}
