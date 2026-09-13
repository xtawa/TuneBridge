package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/xtawa/tunebridge/internal/source"
)

type QRLoginClient interface {
	CreateQRCode(ctx context.Context) (source.QRCode, error)
	CheckQRCode(ctx context.Context, key string) (source.QRLoginStatus, source.Session, error)
}

type SessionSaver interface {
	Save(ctx context.Context, sourceID string, session source.Session) error
}

type NeteaseLoginHandler struct {
	client QRLoginClient
	saver  SessionSaver
}

func NewNeteaseLoginHandler(client QRLoginClient, saver SessionSaver) *NeteaseLoginHandler {
	return &NeteaseLoginHandler{client: client, saver: saver}
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
