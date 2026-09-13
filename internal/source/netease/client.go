package netease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/xtawa/tunebridge/internal/source"
)

const SourceID = "netease"

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	native     *NativeClient
}

func New(baseURL string, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if baseURL == "" || baseURL == DefaultNeteaseBaseURL {
		native, err := NewNativeClient(DefaultNeteaseBaseURL, httpClient)
		if err != nil {
			return nil, err
		}
		parsed, _ := url.Parse(DefaultNeteaseBaseURL)
		return &Client{baseURL: parsed, httpClient: httpClient, native: native}, nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("netease API base URL must be absolute")
	}
	native, _ := NewNativeClient(DefaultNeteaseBaseURL, httpClient)
	return &Client{baseURL: parsed, httpClient: httpClient, native: native}, nil
}

func (c *Client) IsExternal() bool {
	return c.baseURL != nil && c.baseURL.String() != DefaultNeteaseBaseURL
}

func (c *Client) ID() string { return SourceID }

func (c *Client) VerifyCookie(ctx context.Context, rawCookie string) (source.Session, error) {
	if c.native != nil {
		return c.native.VerifyCookie(ctx, rawCookie)
	}
	return source.Session{}, errors.New("native verification client not configured")
}

func (c *Client) RefreshToken(ctx context.Context, session source.Session) (source.Session, error) {
	if c.native != nil && (!c.IsExternal() || c.native.baseURL.String() == c.baseURL.String()) {
		return c.native.RefreshToken(ctx, session)
	}
	return session, nil
}

func (c *Client) CreateQRCode(ctx context.Context) (source.QRCode, error) {
	if !c.IsExternal() && c.native != nil {
		return c.native.CreateQRCode(ctx)
	}
	var keyResponse struct {
		Code int `json:"code"`
		Data struct {
			Key string `json:"unikey"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if _, err := c.get(ctx, "/login/qr/key", nil, "", &keyResponse); err != nil {
		return source.QRCode{}, err
	}
	if keyResponse.Code != http.StatusOK || keyResponse.Data.Key == "" {
		return source.QRCode{}, apiError(keyResponse.Code, keyResponse.Message)
	}

	var createResponse struct {
		Code int `json:"code"`
		Data struct {
			URL   string `json:"qrurl"`
			Image string `json:"qrimg"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if _, err := c.get(ctx, "/login/qr/create", url.Values{"key": {keyResponse.Data.Key}, "qrimg": {"true"}}, "", &createResponse); err != nil {
		return source.QRCode{}, err
	}
	if createResponse.Code != http.StatusOK || createResponse.Data.URL == "" {
		return source.QRCode{}, apiError(createResponse.Code, createResponse.Message)
	}
	return source.QRCode{Key: keyResponse.Data.Key, URL: createResponse.Data.URL, ImageData: createResponse.Data.Image}, nil
}

func (c *Client) CheckQRCode(ctx context.Context, key string) (source.QRLoginStatus, source.Session, error) {
	if strings.TrimSpace(key) == "" {
		return "", source.Session{}, errors.New("QR login key is required")
	}
	if !c.IsExternal() && c.native != nil {
		return c.native.CheckQRCode(ctx, key)
	}
	var response struct {
		Code    int    `json:"code"`
		Cookie  string `json:"cookie"`
		Message string `json:"message"`
	}
	header, err := c.get(ctx, "/login/qr/check", url.Values{"key": {key}}, "", &response)
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
	case 803:
		cookie := response.Cookie
		if cookie == "" {
			cookie = cookieHeader(header.Values("Set-Cookie"))
		}
		if cookie == "" {
			return "", source.Session{}, errors.New("QR login succeeded without a session cookie")
		}
		return source.QRLoginAuthorized, source.Session{Payload: []byte(cookie)}, nil
	default:
		return "", source.Session{}, apiError(response.Code, response.Message)
	}
}

type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("netease API returned code %d", e.Code)
	}
	return fmt.Sprintf("netease API returned code %d: %s", e.Code, e.Message)
}

func apiError(code int, message string) error {
	return &APIError{Code: code, Message: sanitizeMessage(message)}
}

func (c *Client) get(ctx context.Context, endpoint string, query url.Values, cookie string, target any) (http.Header, error) {
	requestURL := *c.baseURL
	requestURL.Path = path.Join(c.baseURL.Path, endpoint)
	values := make(url.Values, len(query)+1)
	for key, items := range query {
		values[key] = append([]string(nil), items...)
	}
	values.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	requestURL.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create netease request: %w", err)
	}
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call netease API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("netease API HTTP status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(target); err != nil {
		return nil, fmt.Errorf("decode netease API response: %w", err)
	}
	return response.Header.Clone(), nil
}

func cookieHeader(setCookies []string) string {
	parts := make([]string, 0, len(setCookies))
	for _, value := range setCookies {
		first, _, _ := strings.Cut(value, ";")
		if strings.Contains(first, "=") {
			parts = append(parts, first)
		}
	}
	return strings.Join(parts, "; ")
}

var _ source.QRLoginSource = (*Client)(nil)
