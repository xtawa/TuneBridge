package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/xtawa/tunebridge/internal/model"
)

type ArtworkService interface {
	Serve(ctx context.Context, w http.ResponseWriter, id model.TrackIdentity) error
}
type ArtworkHandler struct {
	sourceID string
	service  ArtworkService
}

func NewArtworkHandler(sourceID string, service ArtworkService) *ArtworkHandler {
	return &ArtworkHandler{sourceID: sourceID, service: service}
}
func (h *ArtworkHandler) Serve(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil || r.PathValue("sourceID") != h.sourceID || strings.TrimSpace(r.PathValue("trackID")) == "" {
		http.NotFound(w, r)
		return
	}
	err := h.service.Serve(r.Context(), w, model.TrackIdentity{SourceID: h.sourceID, TrackID: r.PathValue("trackID")})
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "artwork is unavailable", http.StatusBadGateway)
	}
}
