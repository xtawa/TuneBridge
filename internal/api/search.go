package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
)

type SearchResultStore interface {
	Add(ctx context.Context, identity model.TrackIdentity) error
	Delete(ctx context.Context, identity model.TrackIdentity) error
	Clear(ctx context.Context, sourceID string) error
}
type SearchInvalidator interface{ InvalidateSearch() }
type SearchHandler struct {
	source      source.MusicSource
	store       SearchResultStore
	invalidator SearchInvalidator
}

func NewSearchHandler(src source.MusicSource, store SearchResultStore, invalidator SearchInvalidator) *SearchHandler {
	return &SearchHandler{source: src, store: store, invalidator: invalidator}
}

func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len([]rune(query)) > 200 {
		writeError(w, http.StatusBadRequest, "q must contain 1 to 200 characters")
		return
	}
	tracks, err := h.source.SearchTracks(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusBadGateway, "search source is unavailable")
		return
	}
	items := make([]searchTrackDTO, 0, len(tracks))
	for _, track := range tracks {
		items = append(items, searchTrackDTO{ID: track.Identity.TrackID, Title: track.Title, Artists: track.Artists, Album: track.AlbumTitle, CoverPath: "/api/artwork/" + url.PathEscape(track.Identity.SourceID) + "/" + url.PathEscape(track.Identity.TrackID)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": query, "tracks": items})
}
func (h *SearchHandler) Add(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request rejected")
		return
	}
	var input struct {
		TrackID string `json:"track_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.TrackID) == "" || len(input.TrackID) > 64 {
		writeError(w, http.StatusBadRequest, "track_id is required")
		return
	}
	if err := h.store.Add(r.Context(), model.TrackIdentity{SourceID: h.source.ID(), TrackID: input.TrackID}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not add search result")
		return
	}
	if h.invalidator != nil {
		h.invalidator.InvalidateSearch()
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "added"})
}
func (h *SearchHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request rejected")
		return
	}
	id := r.PathValue("trackID")
	if strings.TrimSpace(id) == "" || len(id) > 64 {
		writeError(w, http.StatusBadRequest, "invalid track ID")
		return
	}
	if err := h.store.Delete(r.Context(), model.TrackIdentity{SourceID: h.source.ID(), TrackID: id}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove search result")
		return
	}
	if h.invalidator != nil {
		h.invalidator.InvalidateSearch()
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *SearchHandler) Clear(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request rejected")
		return
	}
	if err := h.store.Clear(r.Context(), h.source.ID()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not clear search results")
		return
	}
	if h.invalidator != nil {
		h.invalidator.InvalidateSearch()
	}
	w.WriteHeader(http.StatusNoContent)
}

type searchTrackDTO struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Artists   []string `json:"artists"`
	Album     string   `json:"album"`
	CoverPath string   `json:"cover_path"`
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	raw := r.Header.Get("Origin")
	if raw == "" {
		return true
	}
	origin, err := url.Parse(raw)
	return err == nil && origin.Host == r.Host && (origin.Scheme == "http" || origin.Scheme == "https")
}
