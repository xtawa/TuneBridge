package netease

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/xtawa/tunebridge/internal/source"
)

func TestQRLoginFlow(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/qr/key":
			writeJSON(w, `{"code":200,"data":{"unikey":"key-1"}}`)
		case "/login/qr/create":
			if r.URL.Query().Get("key") != "key-1" || r.URL.Query().Get("qrimg") != "true" {
				t.Fatalf("unexpected QR create query: %s", r.URL.RawQuery)
			}
			writeJSON(w, `{"code":200,"data":{"qrurl":"https://music.163.com/login?code=key-1","qrimg":"data:image/png;base64,abc"}}`)
		case "/login/qr/check":
			if r.URL.Query().Get("key") != "key-1" {
				t.Fatalf("unexpected QR check query: %s", r.URL.RawQuery)
			}
			writeJSON(w, `{"code":803,"cookie":"MUSIC_U=private-session; __csrf=private-csrf"}`)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	qr, err := client.CreateQRCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if qr.Key != "key-1" || qr.URL == "" || qr.ImageData == "" {
		t.Fatalf("unexpected QR code: %#v", qr)
	}
	status, session, err := client.CheckQRCode(context.Background(), qr.Key)
	if err != nil {
		t.Fatal(err)
	}
	if status != source.QRLoginAuthorized || string(session.Payload) != "MUSIC_U=private-session; __csrf=private-csrf" {
		t.Fatalf("unexpected login result: %q %#v", status, session)
	}
}

func TestCheckQRCodeMapsPendingAndExpiredStates(t *testing.T) {
	t.Parallel()
	for code, want := range map[int]source.QRLoginStatus{800: source.QRLoginExpired, 801: source.QRLoginWaiting, 802: source.QRLoginAwaitingConfirmation} {
		code, want := code, want
		t.Run(wantString(want), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, `{"code":`+strconv.Itoa(code)+`}`) }))
			defer server.Close()
			client, err := New(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := client.CheckQRCode(context.Background(), "key")
			if err != nil || got != want {
				t.Fatalf("status=%q err=%v", got, err)
			}
		})
	}
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
func wantString(value source.QRLoginStatus) string { return string(value) }
