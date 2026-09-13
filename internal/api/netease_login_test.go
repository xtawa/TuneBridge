package api

import (
	"context"
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
	if strings.Contains(response.Body.String(), "MUSIC_U") {
		t.Fatal("session payload was exposed")
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
}

type fakeLoginClient struct {
	qr      source.QRCode
	status  source.QRLoginStatus
	session source.Session
}

func (f fakeLoginClient) CreateQRCode(context.Context) (source.QRCode, error) { return f.qr, nil }
func (f fakeLoginClient) CheckQRCode(context.Context, string) (source.QRLoginStatus, source.Session, error) {
	return f.status, f.session, nil
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
