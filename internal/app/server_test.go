package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/xtawa/tunebridge/internal/api"
	"github.com/xtawa/tunebridge/internal/config"
	"github.com/xtawa/tunebridge/internal/database"
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
