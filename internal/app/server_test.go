package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xtawa/tunebridge/internal/api"
	"github.com/xtawa/tunebridge/internal/config"
	"github.com/xtawa/tunebridge/internal/database"
	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
)

func TestHealthAndReadyAreAvailableWithoutWebDAVCredentials(t *testing.T) {
	t.Parallel()
	db, err := database.Open(filepath.Join(t.TempDir(), "tunebridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := NewServer(config.Config{ListenAddress: ":0", WebDAVUsername: "user", WebDAVPassword: "password"}, db, slog.New(slog.NewTextHandler(testWriter{t}, nil)), nil)

	for _, target := range []string{"/healthz", "/readyz"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", target, response.Code, response.Body.String())
		}
	}
}

func TestNeteaseLoginRouteUsesSharedBasicAuthentication(t *testing.T) {
	t.Parallel()
	db, err := database.Open(filepath.Join(t.TempDir(), "tunebridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	login := api.NewNeteaseLoginHandler(appLoginClient{}, appSessionSaver{})
	server := NewServer(config.Config{ListenAddress: ":0", WebDAVUsername: "user", WebDAVPassword: "password"}, db, slog.Default(), login)
	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/sources/netease/login/qr", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/sources/netease/login/qr", nil)
	request.SetBasicAuth("user", "password")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("authorized status = %d: %s", response.Code, response.Body.String())
	}

	// Verify /setup/netease requires Basic Auth and returns HTML when authenticated
	unauthSetup := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthSetup, httptest.NewRequest(http.MethodGet, "/setup/netease", nil))
	if unauthSetup.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /setup/netease, got %d", unauthSetup.Code)
	}

	authSetup := httptest.NewRecorder()
	reqSetup := httptest.NewRequest(http.MethodGet, "/setup/netease", nil)
	reqSetup.SetBasicAuth("user", "password")
	server.Handler().ServeHTTP(authSetup, reqSetup)
	if authSetup.Code != http.StatusOK {
		t.Fatalf("expected 200 for authenticated /setup/netease, got %d", authSetup.Code)
	}

	// Verify /api/sources/netease/status requires Basic Auth
	unauthStatus := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthStatus, httptest.NewRequest(http.MethodGet, "/api/sources/netease/status", nil))
	if unauthStatus.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/sources/netease/status, got %d", unauthStatus.Code)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { return len(p), nil }

type appLoginClient struct{}

func (appLoginClient) CreateQRCode(context.Context) (source.QRCode, error) {
	return source.QRCode{Key: "key", URL: "https://example.test/qr"}, nil
}
func (appLoginClient) CheckQRCode(context.Context, string) (source.QRLoginStatus, source.Session, error) {
	return source.QRLoginWaiting, source.Session{}, nil
}

type appSessionSaver struct{}

func (appSessionSaver) Save(context.Context, string, source.Session) error { return nil }

type appSearchSource struct{}

func (appSearchSource) ID() string                                                               { return "netease" }
func (appSearchSource) Authenticate(context.Context, source.AuthInput) (source.Session, error)   { return source.Session{}, nil }
func (appSearchSource) RefreshSession(context.Context, source.Session) (source.Session, error)  { return source.Session{}, nil }
func (appSearchSource) UserProfile(context.Context) (source.UserProfile, error)                  { return source.UserProfile{}, nil }
func (appSearchSource) LikedTracks(context.Context) ([]model.Track, error)                       { return nil, nil }
func (appSearchSource) LikeTrack(context.Context, string, bool) error                            { return nil }
func (appSearchSource) Playlists(context.Context) ([]source.Playlist, error)                     { return nil, nil }
func (appSearchSource) Playlist(context.Context, string) (source.Playlist, []model.Track, error) { return source.Playlist{}, nil, nil }
func (appSearchSource) DailyRecommendations(context.Context) ([]model.Track, error)             { return nil, nil }
func (appSearchSource) SearchTracks(context.Context, string) ([]model.Track, error)              { return nil, nil }
func (appSearchSource) Track(context.Context, model.TrackIdentity) (model.Track, error)          { return model.Track{}, nil }
func (appSearchSource) Cover(context.Context, model.TrackIdentity) (model.Cover, error)          { return model.Cover{}, nil }
func (appSearchSource) Lyrics(context.Context, model.TrackIdentity) (model.Lyrics, error)        { return model.Lyrics{}, nil }
func (appSearchSource) ResolveStream(context.Context, model.TrackIdentity, source.Quality) (model.ResolvedStream, error) {
	return model.ResolvedStream{}, nil
}

type appSearchStore struct{}

func (appSearchStore) Add(context.Context, model.TrackIdentity) error { return nil }
func (appSearchStore) Delete(context.Context, model.TrackIdentity) error { return nil }
func (appSearchStore) Clear(context.Context, string) error { return nil }

type appInvalidator struct {
	invalidatedLiked atomic.Bool
}

func (a *appInvalidator) InvalidateSearch() {}
func (a *appInvalidator) InvalidateLiked()  { a.invalidatedLiked.Store(true) }

func TestLikeRoute_AuthenticationAndSuccess(t *testing.T) {
	t.Parallel()
	db, err := database.Open(filepath.Join(t.TempDir(), "tunebridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	inv := &appInvalidator{}
	searchHandler := api.NewSearchHandler(appSearchSource{}, appSearchStore{}, inv)
	server := NewServer(config.Config{ListenAddress: ":0", WebDAVUsername: "user", WebDAVPassword: "password"}, db, slog.Default(), nil)
	server = NewServerWithLibrary(config.Config{ListenAddress: ":0", WebDAVUsername: "user", WebDAVPassword: "password"}, db, slog.Default(), nil, searchHandler, nil, nil)

	// 1. Unauthenticated request
	unauthReq := httptest.NewRequest(http.MethodPost, "/api/sources/netease/like", strings.NewReader(`{"track_id":"12345","like":true}`))
	unauthReq.Header.Set("Content-Type", "application/json")
	unauthRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthRec.Code)
	}

	// 2. Authenticated request
	authReq := httptest.NewRequest(http.MethodPost, "/api/sources/netease/like", strings.NewReader(`{"track_id":"12345","like":true}`))
	authReq.SetBasicAuth("user", "password")
	authReq.Header.Set("Content-Type", "application/json")
	authRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", authRec.Code, authRec.Body.String())
	}
	if !strings.Contains(authRec.Body.String(), `"status":"liked"`) {
		t.Fatalf("unexpected body: %s", authRec.Body.String())
	}
	if !inv.invalidatedLiked.Load() {
		t.Fatalf("expected InvalidateLiked to have been called")
	}
}
