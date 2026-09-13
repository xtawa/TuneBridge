package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/xtawa/tunebridge/internal/source"
)

type QRLoginClient interface {
	CreateQRCode(ctx context.Context) (source.QRCode, error)
	CheckQRCode(ctx context.Context, key string) (source.QRLoginStatus, source.Session, error)
}

type CookieVerifier interface {
	VerifyCookie(ctx context.Context, cookie string) (source.Session, error)
}

type SessionSaver interface {
	Save(ctx context.Context, sourceID string, session source.Session) error
}

type SessionLoader interface {
	Load(ctx context.Context, sourceID string) (source.Session, error)
}

type NeteaseLoginHandler struct {
	client   QRLoginClient
	verifier CookieVerifier
	saver    SessionSaver
	loader   SessionLoader
}

func NewNeteaseLoginHandler(client QRLoginClient, saver SessionSaver) *NeteaseLoginHandler {
	var verifier CookieVerifier
	if v, ok := client.(CookieVerifier); ok {
		verifier = v
	}
	var loader SessionLoader
	if l, ok := saver.(SessionLoader); ok {
		loader = l
	}
	return &NeteaseLoginHandler{client: client, verifier: verifier, saver: saver, loader: loader}
}

func NewNeteaseLoginHandlerWithFallback(client QRLoginClient, verifier CookieVerifier, saver SessionSaver, loader SessionLoader) *NeteaseLoginHandler {
	return &NeteaseLoginHandler{client: client, verifier: verifier, saver: saver, loader: loader}
}

func (h *NeteaseLoginHandler) Begin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	qr, err := h.client.CreateQRCode(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not create login QR code")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"key": qr.Key, "url": qr.URL, "image_data": qr.ImageData})
}

func (h *NeteaseLoginHandler) Check(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	key := r.PathValue("key")
	if strings.TrimSpace(key) == "" || strings.Contains(key, "/") {
		writeError(w, http.StatusBadRequest, "invalid QR login key")
		return
	}
	status, session, err := h.client.CheckQRCode(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not check login QR code")
		return
	}
	if status == source.QRLoginAuthorized {
		if err := h.saver.Save(r.Context(), "netease", session); err != nil {
			writeError(w, http.StatusInternalServerError, "could not persist login session")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(status)})
}

func (h *NeteaseLoginHandler) ImportCookie(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.verifier == nil {
		writeError(w, http.StatusNotImplemented, "cookie import is not supported")
		return
	}
	var body struct {
		Cookie string `json:"cookie"`
		MusicU string `json:"music_u"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	raw := strings.TrimSpace(body.Cookie)
	if raw == "" {
		raw = strings.TrimSpace(body.MusicU)
	}
	if raw == "" {
		writeError(w, http.StatusBadRequest, "cookie or music_u is required")
		return
	}
	session, err := h.verifier.VerifyCookie(r.Context(), raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cookie verification failed: invalid or expired session")
		return
	}
	if err := h.saver.Save(r.Context(), "netease", session); err != nil {
		writeError(w, http.StatusInternalServerError, "could not persist login session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "authorized"})
}

func (h *NeteaseLoginHandler) Status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if h.loader == nil {
		writeJSON(w, http.StatusOK, map[string]any{"logged_in": false})
		return
	}
	session, err := h.loader.Load(r.Context(), "netease")
	if err != nil || len(session.Payload) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"logged_in": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logged_in": true})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
