package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xtawa/tunebridge/internal/source"
)

func TestAuthorizedQRLoginPersistsSessionWithoutReturningIt(t *testing.T) {
	t.Parallel()
	client := fakeLoginClient{status: source.QRLoginAuthorized, session: source.Session{Payload: []byte("MUSIC_U=secret")}}
	saver := &fakeSaver{}
	handler := NewNeteaseLoginHandler(client, saver)
	request := httptest.NewRequest(http.MethodGet, "/api/sources/netease/login/qr/key-1", nil)
	request.SetPathValue("key", "key-1")
	response := httptest.NewRecorder()
	handler.Check(response, request)
	if response.Code != http.StatusOK || saver.sourceID != "netease" || string(saver.session.Payload) != "MUSIC_U=secret" {
		t.Fatalf("unexpected response/persistence: %d %#v", response.Code, saver)
	}
	if strings.Contains(response.Body.String(), "MUSIC_U") || strings.Contains(response.Body.String(), "secret") {
		t.Fatal("session payload was exposed in response")
	}
}

func TestBeginReturnsQRCodeButRequiresPOST(t *testing.T) {
	t.Parallel()
	handler := NewNeteaseLoginHandler(fakeLoginClient{qr: source.QRCode{Key: "key", URL: "https://example.test/qr"}}, &fakeSaver{})
	response := httptest.NewRecorder()
	handler.Begin(response, httptest.NewRequest(http.MethodPost, "/api/sources/netease/login/qr", nil))
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), "https://example.test/qr") {
		t.Fatalf("unexpected begin response: %d %s", response.Code, response.Body.String())
	}

	getRes := httptest.NewRecorder()
	handler.Begin(getRes, httptest.NewRequest(http.MethodGet, "/api/sources/netease/login/qr", nil))
	if getRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET on begin, got %d", getRes.Code)
	}
}

func TestImportCookie_Success(t *testing.T) {
	t.Parallel()
	client := &fakeLoginClient{}
	saver := &fakeSaver{}
	handler := NewNeteaseLoginHandler(client, saver)

	body := `{"cookie":"MUSIC_U=imported-token; __csrf=xyz"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sources/netease/login/cookie", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ImportCookie(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if saver.sourceID != "netease" || string(saver.session.Payload) != "MUSIC_U=imported-token; __csrf=xyz" {
		t.Fatalf("unexpected saved session: %#v", saver)
	}
	// Zero sensitive leak assertion
	if strings.Contains(rec.Body.String(), "imported-token") || strings.Contains(rec.Body.String(), "xyz") {
		t.Fatal("sensitive token leaked in response body")
	}
}

func TestImportCookie_BareMusicUField(t *testing.T) {
	t.Parallel()
	client := &fakeLoginClient{}
	saver := &fakeSaver{}
	handler := NewNeteaseLoginHandler(client, saver)

	body := `{"music_u":"bare_music_u_token"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sources/netease/login/cookie", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ImportCookie(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(saver.session.Payload), "bare_music_u_token") {
		t.Fatalf("expected session to contain token, got %s", saver.session.Payload)
	}
	if strings.Contains(rec.Body.String(), "bare_music_u_token") {
		t.Fatal("sensitive music_u leaked in response body")
	}
}

func TestImportCookie_VerificationFailure(t *testing.T) {
	t.Parallel()
	client := &fakeLoginClient{verifyErr: errors.New("verification rejected")}
	saver := &fakeSaver{}
	handler := NewNeteaseLoginHandler(client, saver)

	body := `{"cookie":"MUSIC_U=invalid_token"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sources/netease/login/cookie", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ImportCookie(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400, got %d", rec.Code)
	}
	if len(saver.session.Payload) > 0 {
		t.Fatal("session must not be saved when verification fails")
	}
	if strings.Contains(rec.Body.String(), "invalid_token") {
		t.Fatal("cookie leaked in error response")
	}
}

func TestImportCookie_MissingBody(t *testing.T) {
	t.Parallel()
	client := &fakeLoginClient{}
	saver := &fakeSaver{}
	handler := NewNeteaseLoginHandler(client, saver)

	body := `{"cookie":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/sources/netease/login/cookie", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ImportCookie(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400, got %d", rec.Code)
	}
}

func TestStatus_ReportsLoginState(t *testing.T) {
	t.Parallel()
	saver := &fakeSaver{}
	handler := NewNeteaseLoginHandler(&fakeLoginClient{}, saver)

	// Initially not logged in
	req1 := httptest.NewRequest(http.MethodGet, "/api/sources/netease/status", nil)
	rec1 := httptest.NewRecorder()
	handler.Status(rec1, req1)
	if rec1.Code != http.StatusOK || !strings.Contains(rec1.Body.String(), `"logged_in":false`) {
		t.Fatalf("expected logged_in false, got %s", rec1.Body.String())
	}

	// After session is saved
	saver.session = source.Session{Payload: []byte("MUSIC_U=valid")}
	req2 := httptest.NewRequest(http.MethodGet, "/api/sources/netease/status", nil)
	rec2 := httptest.NewRecorder()
	handler.Status(rec2, req2)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `"logged_in":true`) {
		t.Fatalf("expected logged_in true, got %s", rec2.Body.String())
	}
	// Zero sensitive leak
	if strings.Contains(rec2.Body.String(), "MUSIC_U") || strings.Contains(rec2.Body.String(), "valid") {
		t.Fatal("session payload leaked in status endpoint")
	}
}

type fakeLoginClient struct {
	qr        source.QRCode
	status    source.QRLoginStatus
	session   source.Session
	verifyErr error
}

func (f fakeLoginClient) CreateQRCode(context.Context) (source.QRCode, error) { return f.qr, nil }
func (f fakeLoginClient) CheckQRCode(context.Context, string) (source.QRLoginStatus, source.Session, error) {
	return f.status, f.session, nil
}
func (f fakeLoginClient) VerifyCookie(_ context.Context, cookie string) (source.Session, error) {
	if f.verifyErr != nil {
		return source.Session{}, f.verifyErr
	}
	return source.Session{Payload: []byte(cookie)}, nil
}

type fakeSaver struct {
	sourceID string
	session  source.Session
}

func (f *fakeSaver) Save(_ context.Context, sourceID string, session source.Session) error {
	f.sourceID = sourceID
	f.session = session
	return nil
}

func (f *fakeSaver) Load(_ context.Context, sourceID string) (source.Session, error) {
	if f.session.Payload == nil {
		return source.Session{}, errors.New("not found")
	}
	return f.session, nil
}
