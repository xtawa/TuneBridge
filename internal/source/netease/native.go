package netease

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"github.com/xtawa/tunebridge/internal/source"
)

const (
	DefaultNeteaseBaseURL = "https://music.163.com"
	DefaultUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// NativeClient implements native Netease Cloud Music authentication directly
// against music.163.com without requiring external NeteaseCloudMusicApi services.
type NativeClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	jar        http.CookieJar
	mu         sync.Mutex
}

func NewNativeClient(customBaseURL string, httpClient *http.Client) (*NativeClient, error) {
	targetURL := DefaultNeteaseBaseURL
	if customBaseURL != "" {
		targetURL = customBaseURL
	}
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("netease base URL must be an absolute URL")
	}

	var jar http.CookieJar
	if httpClient != nil && httpClient.Jar != nil {
		jar = httpClient.Jar
	} else {
		var jarErr error
		jar, jarErr = cookiejar.New(nil)
		if jarErr != nil {
			return nil, fmt.Errorf("create cookie jar: %w", jarErr)
		}
	}

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 15 * time.Second,
			Jar:     jar,
		}
	} else if httpClient.Jar == nil {
		httpClient.Jar = jar
	}

	return &NativeClient{
		baseURL:    parsed,
		httpClient: httpClient,
		jar:        jar,
	}, nil
}

func (c *NativeClient) ID() string { return SourceID }

// CreateQRCode requests a new QR key from music.163.com and generates a PNG QR code image.
func (c *NativeClient) CreateQRCode(ctx context.Context) (source.QRCode, error) {
	var keyResponse struct {
		Code   int    `json:"code"`
		UniKey string `json:"unikey"`
		Msg    string `json:"msg"`
	}

	form := url.Values{"type": {"1"}}
	if _, err := c.postForm(ctx, "/api/login/qrcode/unikey", form, &keyResponse); err != nil {
		return source.QRCode{}, err
	}
	if keyResponse.Code != http.StatusOK || keyResponse.UniKey == "" {
		return source.QRCode{}, apiError(keyResponse.Code, keyResponse.Msg)
	}

	qrURL := fmt.Sprintf("https://music.163.com/login?codekey=%s", keyResponse.UniKey)
	pngBytes, err := qrcode.Encode(qrURL, qrcode.Medium, 256)
	if err != nil {
		return source.QRCode{}, fmt.Errorf("encode QR code image: %w", err)
	}
	imageData := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

	return source.QRCode{
		Key:       keyResponse.UniKey,
		URL:       qrURL,
		ImageData: imageData,
	}, nil
}

// CheckQRCode polls the status of a QR login key, performs secondary verification
// in the same CookieJar upon code 803, and returns the session.
func (c *NativeClient) CheckQRCode(ctx context.Context, key string) (source.QRLoginStatus, source.Session, error) {
	if strings.TrimSpace(key) == "" {
		return "", source.Session{}, errors.New("QR login key is required")
	}

	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Cookie  string `json:"cookie"`
	}

	form := url.Values{
		"type": {"1"},
		"key":  {key},
	}
	header, err := c.postForm(ctx, "/api/login/qrcode/client/login", form, &response)
	if err != nil {
		return "", source.Session{}, err
	}

	switch response.Code {
	case 800:
		return source.QRLoginExpired, source.Session{}, nil
	case 801:
		return source.QRLoginWaiting, source.Session{}, nil
	case 802:
		return source.QRLoginAwaitingConfirmation, source.Session{}, nil
	case 804:
		return "", source.Session{}, errors.New("authorization cancelled by user")
	case 803:
		// Succeeded: extract cookies from Jar or Set-Cookie headers
		c.mu.Lock()
		defer c.mu.Unlock()

		// Merge cookies from Set-Cookie or response body if any
		if response.Cookie != "" {
			c.populateJarFromCookieString(response.Cookie)
		}
		if setCookies := header.Values("Set-Cookie"); len(setCookies) > 0 {
			c.populateJarFromSetCookie(setCookies)
		}

		// Perform secondary verification using the same CookieJar
		profile, err := c.verifyAccountInJar(ctx)
		if err != nil {
			return "", source.Session{}, fmt.Errorf("secondary verification failed: %w", err)
		}
		if profile.ID == "" {
			return "", source.Session{}, errors.New("secondary verification failed: user account not found")
		}

		// Extract final session cookie string
		cookieStr := c.cookieStringFromJar()
		if !strings.Contains(cookieStr, "MUSIC_U") {
			return "", source.Session{}, errors.New("QR login succeeded without MUSIC_U cookie")
		}

		return source.QRLoginAuthorized, source.Session{
			Payload: []byte(cookieStr),
		}, nil
	default:
		return "", source.Session{}, apiError(response.Code, response.Message)
	}
}

// VerifyCookie verifies an imported cookie string (or raw MUSIC_U token) against
// the Netease account API and returns a valid session on success.
func (c *NativeClient) VerifyCookie(ctx context.Context, rawCookie string) (source.Session, error) {
	normalized := NormalizeCookie(rawCookie)
	if normalized == "" {
		return source.Session{}, errors.New("cookie must not be empty")
	}
	if !strings.Contains(normalized, "MUSIC_U=") {
		return source.Session{}, errors.New("cookie is missing MUSIC_U")
	}

	profile, err := c.verifyAccountWithCookie(ctx, normalized)
	if err != nil {
		return source.Session{}, fmt.Errorf("cookie verification failed: %w", err)
	}
	if profile.ID == "" {
		return source.Session{}, errors.New("cookie verification failed: invalid or expired session")
	}

	return source.Session{
		Payload: []byte(normalized),
	}, nil
}

// NormalizeCookie handles raw tokens (e.g. "abcdef1234") and full cookie strings.
func NormalizeCookie(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	if !strings.Contains(cleaned, "=") {
		// User entered a bare MUSIC_U token value
		return "MUSIC_U=" + cleaned
	}
	// Parse and clean cookie parts
	parts := strings.Split(cleaned, ";")
	var valid []string
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" && strings.Contains(trimmed, "=") {
			valid = append(valid, trimmed)
		}
	}
	return strings.Join(valid, "; ")
}

type accountResponse struct {
	Code    int `json:"code"`
	Account *struct {
		ID       flexibleID `json:"id"`
		UserName string     `json:"userName"`
		Status   int        `json:"status"`
	} `json:"account"`
	Profile *struct {
		UserID   flexibleID `json:"userId"`
		Nickname string     `json:"nickname"`
	} `json:"profile"`
	Message string `json:"message"`
}

func (c *NativeClient) verifyAccountInJar(ctx context.Context) (source.UserProfile, error) {
	var resp accountResponse
	_, err := c.postForm(ctx, "/api/nuser/account/get", nil, &resp)
	if err != nil {
		return source.UserProfile{}, err
	}
	if resp.Code != http.StatusOK {
		return source.UserProfile{}, apiError(resp.Code, resp.Message)
	}
	if resp.Account == nil && resp.Profile == nil {
		return source.UserProfile{}, errors.New("no account profile returned")
	}

	var userID string
	var nickname string
	if resp.Profile != nil {
		userID = resp.Profile.UserID.String()
		nickname = resp.Profile.Nickname
	}
	if userID == "" && resp.Account != nil {
		userID = resp.Account.ID.String()
		nickname = resp.Account.UserName
	}

	return source.UserProfile{
		ID:          userID,
		DisplayName: nickname,
	}, nil
}

func (c *NativeClient) verifyAccountWithCookie(ctx context.Context, cookie string) (source.UserProfile, error) {
	reqURL := *c.baseURL
	reqURL.Path = path.Join(c.baseURL.Path, "/api/nuser/account/get")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), nil)
	if err != nil {
		return source.UserProfile{}, fmt.Errorf("create verification request: %w", err)
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Referer", c.baseURL.String())
	req.Header.Set("Cookie", cookie)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return source.UserProfile{}, fmt.Errorf("call verification API: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return source.UserProfile{}, fmt.Errorf("verification API returned HTTP %d", res.StatusCode)
	}

	var resp accountResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&resp); err != nil {
		return source.UserProfile{}, fmt.Errorf("decode verification response: %w", err)
	}
	if resp.Code != http.StatusOK {
		return source.UserProfile{}, apiError(resp.Code, resp.Message)
	}
	if resp.Account == nil && resp.Profile == nil {
		return source.UserProfile{}, errors.New("no account profile returned")
	}

	var userID string
	var nickname string
	if resp.Profile != nil {
		userID = resp.Profile.UserID.String()
		nickname = resp.Profile.Nickname
	}
	if userID == "" && resp.Account != nil {
		userID = resp.Account.ID.String()
		nickname = resp.Account.UserName
	}

	return source.UserProfile{
		ID:          userID,
		DisplayName: nickname,
	}, nil
}

func (c *NativeClient) postForm(ctx context.Context, endpoint string, form url.Values, target any) (http.Header, error) {
	reqURL := *c.baseURL
	reqURL.Path = path.Join(c.baseURL.Path, endpoint)

	var bodyReader io.Reader
	if form != nil {
		bodyReader = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create netease request: %w", err)
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Referer", c.baseURL.String())
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call netease API: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("netease API HTTP status %d", res.StatusCode)
	}

	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(target); err != nil {
		return nil, fmt.Errorf("decode netease API response: %w", err)
	}

	return res.Header.Clone(), nil
}

func (c *NativeClient) cookieStringFromJar() string {
	cookies := c.jar.Cookies(c.baseURL)
	parts := make([]string, 0, len(cookies))
	for _, ck := range cookies {
		if ck.Name != "" && ck.Value != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", ck.Name, ck.Value))
		}
	}
	return strings.Join(parts, "; ")
}

func (c *NativeClient) populateJarFromSetCookie(setCookies []string) {
	dummyReq := &http.Response{Header: http.Header{"Set-Cookie": setCookies}}
	parsedCookies := dummyReq.Cookies()
	c.jar.SetCookies(c.baseURL, parsedCookies)
}

func (c *NativeClient) populateJarFromCookieString(raw string) {
	var cookies []*http.Cookie
	parts := strings.Split(raw, ";")
	for _, part := range parts {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k != "" {
			cookies = append(cookies, &http.Cookie{
				Name:  k,
				Value: v,
			})
		}
	}
	if len(cookies) > 0 {
		c.jar.SetCookies(c.baseURL, cookies)
	}
}
