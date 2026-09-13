package netease

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtawa/tunebridge/internal/source"
)

func TestNativeQRLogin_CreateQRCode(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/login/qrcode/unikey" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("type") != "1" {
			t.Fatalf("unexpected form body: %v", r.Form)
		}
		writeJSON(w, `{"code":200,"unikey":"native-key-123"}`)
	}))
	defer server.Close()

	client, err := NewNativeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	qr, err := client.CreateQRCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if qr.Key != "native-key-123" {
		t.Fatalf("expected key 'native-key-123', got %q", qr.Key)
	}
	expectedURL := "https://music.163.com/login?codekey=native-key-123"
	if qr.URL != expectedURL {
		t.Fatalf("expected url %q, got %q", expectedURL, qr.URL)
	}
	if !strings.HasPrefix(qr.ImageData, "data:image/png;base64,") {
		t.Fatalf("expected data:image/png;base64 prefix, got %q", qr.ImageData)
	}
	rawBase64 := strings.TrimPrefix(qr.ImageData, "data:image/png;base64,")
	decoded, err := base64.StdEncoding.DecodeString(rawBase64)
	if err != nil || len(decoded) == 0 {
		t.Fatalf("invalid generated QR PNG image data: %v", err)
	}
}

func TestNativeQRLogin_StateCodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		code       int
		wantStatus source.QRLoginStatus
		wantErr    string
	}{
		{name: "waiting_801", code: 801, wantStatus: source.QRLoginWaiting},
		{name: "awaiting_confirmation_802", code: 802, wantStatus: source.QRLoginAwaitingConfirmation},
		{name: "expired_800", code: 800, wantStatus: source.QRLoginExpired},
		{name: "cancelled_804", code: 804, wantErr: "authorization cancelled by user"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/login/qrcode/client/login" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				writeJSON(w, `{"code":`+string(rune('0'+tc.code/100))+string(rune('0'+(tc.code%100)/10))+string(rune('0'+tc.code%10))+`,"message":"msg"}`)
			}))
			defer server.Close()

			client, err := NewNativeClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			status, _, err := client.CheckQRCode(context.Background(), "test-key")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != tc.wantStatus {
					t.Fatalf("expected status %q, got %q", tc.wantStatus, status)
				}
			}
		})
	}
}

func TestNativeQRLogin_AuthorizedSuccessWithSecondaryVerification(t *testing.T) {
	t.Parallel()
	var accountChecked atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/qrcode/client/login":
			http.SetCookie(w, &http.Cookie{
				Name:  "MUSIC_U",
				Value: "native-music-u-token",
				Path:  "/",
			})
			http.SetCookie(w, &http.Cookie{
				Name:  "__csrf",
				Value: "native-csrf-token",
				Path:  "/",
			})
			writeJSON(w, `{"code":803,"message":"授权成功"}`)
		case "/api/nuser/account/get":
			accountChecked.Store(true)
			// Verify cookie received in the same CookieJar
			cookie := r.Header.Get("Cookie")
			if !strings.Contains(cookie, "MUSIC_U=native-music-u-token") {
				t.Fatalf("secondary verification missing MUSIC_U cookie: %s", cookie)
			}
			writeJSON(w, `{"code":200,"account":{"id":999888,"userName":"tester"},"profile":{"userId":999888,"nickname":"TesterName"}}`)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewNativeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	status, session, err := client.CheckQRCode(context.Background(), "good-key")
	if err != nil {
		t.Fatal(err)
	}
	if status != source.QRLoginAuthorized {
		t.Fatalf("expected status %s, got %s", source.QRLoginAuthorized, status)
	}
	if !accountChecked.Load() {
		t.Fatal("secondary account verification was not invoked")
	}
	payload := string(session.Payload)
	if !strings.Contains(payload, "MUSIC_U=native-music-u-token") {
		t.Fatalf("session payload missing MUSIC_U: %s", payload)
	}
	if !strings.Contains(payload, "__csrf=native-csrf-token") {
		t.Fatalf("session payload missing __csrf: %s", payload)
	}
}

func TestNativeQRLogin_SuccessButCookieInvalidInSecondaryVerification(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/qrcode/client/login":
			http.SetCookie(w, &http.Cookie{
				Name:  "MUSIC_U",
				Value: "bogus-music-u",
				Path:  "/",
			})
			writeJSON(w, `{"code":803,"message":"授权成功"}`)
		case "/api/nuser/account/get":
			// Secondary verification reports null account / invalid cookie
			writeJSON(w, `{"code":200,"account":null,"profile":null}`)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewNativeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	status, session, err := client.CheckQRCode(context.Background(), "invalid-cookie-key")
	if err == nil {
		t.Fatalf("expected error for invalid cookie, got status=%s, session=%v", status, session)
	}
	if status == source.QRLoginAuthorized || len(session.Payload) > 0 {
		t.Fatalf("must not return authorized session on verification failure, got status=%s", status)
	}
	if !strings.Contains(err.Error(), "secondary verification failed") {
		t.Fatalf("expected secondary verification error, got %v", err)
	}
}

func TestNativeQRLogin_SuccessWithoutCookie(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login/qrcode/client/login" {
			writeJSON(w, `{"code":803,"message":"授权成功"}`)
		} else {
			writeJSON(w, `{"code":200,"account":null,"profile":null}`)
		}
	}))
	defer server.Close()

	client, err := NewNativeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = client.CheckQRCode(context.Background(), "no-cookie-key")
	if err == nil {
		t.Fatal("expected error when 803 returns without cookies")
	}
}

func TestNativeQRLogin_TimeoutAndContextCancel(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(w, `{"code":801}`)
	}))
	defer server.Close()

	client, err := NewNativeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, _, err = client.CheckQRCode(ctx, "timeout-key")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestNativeQRLogin_RepeatedScanStability(t *testing.T) {
	t.Parallel()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login/qrcode/client/login":
			c := atomic.AddInt32(&calls, 1)
			if c == 1 {
				writeJSON(w, `{"code":801,"message":"等待扫码"}`)
			} else {
				http.SetCookie(w, &http.Cookie{Name: "MUSIC_U", Value: "stable-music-u", Path: "/"})
				writeJSON(w, `{"code":803,"message":"已授权"}`)
			}
		case "/api/nuser/account/get":
			writeJSON(w, `{"code":200,"account":{"id":123,"userName":"user123"},"profile":{"userId":123,"nickname":"Nick"}}`)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewNativeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	// First poll: waiting
	status1, _, err := client.CheckQRCode(context.Background(), "repeated-key")
	if err != nil || status1 != source.QRLoginWaiting {
		t.Fatalf("first poll unexpected: status=%s, err=%v", status1, err)
	}

	// Second poll: authorized
	status2, session2, err := client.CheckQRCode(context.Background(), "repeated-key")
	if err != nil || status2 != source.QRLoginAuthorized {
		t.Fatalf("second poll unexpected: status=%s, err=%v", status2, err)
	}
	if len(session2.Payload) == 0 {
		t.Fatal("expected session on second poll")
	}

	// Third poll: repeated check remains stable and does not panic
	status3, session3, err := client.CheckQRCode(context.Background(), "repeated-key")
	if err != nil || status3 != source.QRLoginAuthorized {
		t.Fatalf("third poll unexpected: status=%s, err=%v", status3, err)
	}
	if string(session3.Payload) != string(session2.Payload) {
		t.Fatalf("session payload changed on repeated scan: %s vs %s", session3.Payload, session2.Payload)
	}
}

func TestNativeClient_VerifyCookie(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nuser/account/get" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		cookie := r.Header.Get("Cookie")
		if strings.Contains(cookie, "MUSIC_U=valid-u") {
			writeJSON(w, `{"code":200,"account":{"id":777,"userName":"importUser"},"profile":{"userId":777,"nickname":"ImportNick"}}`)
		} else {
			writeJSON(w, `{"code":200,"account":null,"profile":null}`)
		}
	}))
	defer server.Close()

	client, err := NewNativeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	// Success case with raw token
	session, err := client.VerifyCookie(context.Background(), "valid-u")
	if err != nil {
		t.Fatalf("unexpected error verifying bare MUSIC_U token: %v", err)
	}
	if string(session.Payload) != "MUSIC_U=valid-u" {
		t.Fatalf("unexpected normalized session payload: %s", session.Payload)
	}

	// Success case with full cookie
	sessionFull, err := client.VerifyCookie(context.Background(), "MUSIC_U=valid-u; __csrf=abc")
	if err != nil {
		t.Fatalf("unexpected error verifying full cookie: %v", err)
	}
	if string(sessionFull.Payload) != "MUSIC_U=valid-u; __csrf=abc" {
		t.Fatalf("unexpected normalized full session payload: %s", sessionFull.Payload)
	}

	// Failure case with invalid cookie
	_, err = client.VerifyCookie(context.Background(), "MUSIC_U=invalid-u")
	if err == nil {
		t.Fatal("expected error verifying invalid cookie")
	}

	// Failure case without MUSIC_U
	_, err = client.VerifyCookie(context.Background(), "__csrf=only_csrf")
	if err == nil || !strings.Contains(err.Error(), "MUSIC_U") {
		t.Fatalf("expected missing MUSIC_U error, got %v", err)
	}
}

func TestNativeClient_ZeroSensitiveDataLeakageInErrors(t *testing.T) {
	t.Parallel()
	sensitiveMUSICU := "SECRET_MUSIC_U_VERY_LONG_TOKEN_99999"
	sensitiveCSRF := "SECRET_CSRF_77777"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"code":400,"message":"bad token"}`)
	}))
	defer server.Close()

	client, err := NewNativeClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.VerifyCookie(context.Background(), "MUSIC_U="+sensitiveMUSICU+"; __csrf="+sensitiveCSRF)
	if err == nil {
		t.Fatal("expected error")
	}
	errStr := err.Error()
	if strings.Contains(errStr, sensitiveMUSICU) {
		t.Fatalf("sensitive MUSIC_U leaked in error string: %s", errStr)
	}
	if strings.Contains(errStr, sensitiveCSRF) {
		t.Fatalf("sensitive __csrf leaked in error string: %s", errStr)
	}
}
