package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtawa/tunebridge/internal/cache"
	"github.com/xtawa/tunebridge/internal/database"
	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
)

const testPayloadSize = 1000

func newDeterministicPayload() []byte {
	payload := make([]byte, testPayloadSize)
	for i := 0; i < testPayloadSize; i++ {
		payload[i] = byte((i*7 + 13) % 251)
	}
	return payload
}

type fakeSource struct {
	resolveFn func(ctx context.Context, id model.TrackIdentity, quality source.Quality) (model.ResolvedStream, error)
}

func (f *fakeSource) ID() string { return "fake" }
func (f *fakeSource) Authenticate(context.Context, source.AuthInput) (source.Session, error) {
	return source.Session{}, nil
}
func (f *fakeSource) RefreshSession(context.Context, source.Session) (source.Session, error) {
	return source.Session{}, nil
}
func (f *fakeSource) UserProfile(context.Context) (source.UserProfile, error) {
	return source.UserProfile{}, nil
}
func (f *fakeSource) LikedTracks(context.Context) ([]model.Track, error)   { return nil, nil }
func (f *fakeSource) Playlists(context.Context) ([]source.Playlist, error) { return nil, nil }
func (f *fakeSource) Playlist(context.Context, string) (source.Playlist, []model.Track, error) {
	return source.Playlist{}, nil, nil
}
func (f *fakeSource) DailyRecommendations(context.Context) ([]model.Track, error) { return nil, nil }
func (f *fakeSource) SearchTracks(context.Context, string) ([]model.Track, error) { return nil, nil }
func (f *fakeSource) Track(context.Context, model.TrackIdentity) (model.Track, error) {
	return model.Track{}, nil
}
func (f *fakeSource) Cover(context.Context, model.TrackIdentity) (model.Cover, error) {
	return model.Cover{}, nil
}
func (f *fakeSource) Lyrics(context.Context, model.TrackIdentity) (model.Lyrics, error) {
	return model.Lyrics{}, nil
}
func (f *fakeSource) ResolveStream(ctx context.Context, id model.TrackIdentity, quality source.Quality) (model.ResolvedStream, error) {
	if f.resolveFn != nil {
		return f.resolveFn(ctx, id, quality)
	}
	return model.ResolvedStream{}, errors.New("not implemented")
}

type upstreamServerFixture struct {
	server       *httptest.Server
	requestCount atomic.Int64
	ignoreRange  atomic.Bool
}

type proxyFixture struct {
	proxy    *Proxy
	source   *fakeSource
	upstream *upstreamServerFixture
	payload  []byte
	trackID  model.TrackIdentity
	desc     model.ResolvedStream
	cache    *cache.Manager
}

func setupProxyFixture(t *testing.T) *proxyFixture {
	t.Helper()
	payload := newDeterministicPayload()

	upFixture := &upstreamServerFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upFixture.requestCount.Add(1)

		if upFixture.ignoreRange.Load() {
			w.Header().Set("Content-Type", "audio/flac")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Header().Set("Content-Type", "audio/flac")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}

		byteRange, err := ParseRange(rangeHeader, int64(len(payload)))
		if err != nil {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(payload)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}

		w.Header().Set("Content-Type", "audio/flac")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", byteRange.Start, byteRange.End, len(payload)))
		w.Header().Set("Content-Length", strconv.FormatInt(byteRange.Length(), 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[byteRange.Start : byteRange.End+1])
	}))
	upFixture.server = server
	t.Cleanup(func() {
		server.Close()
	})

	dbPath := filepath.Join(t.TempDir(), "tunebridge.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("database.Open failed: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	cacheDir := t.TempDir()
	audioCache, err := cache.NewAudioManager(db, cacheDir, 10*1024*1024)
	if err != nil {
		t.Fatalf("cache.NewAudioManager failed: %v", err)
	}

	trackID := model.TrackIdentity{SourceID: "fake-source", TrackID: "test-track-1"}
	desc := model.ResolvedStream{
		URL:       server.URL + "/stream/audio.flac?signature=fake-secret-token",
		ExpiresAt: time.Now().Add(1 * time.Hour),
		Size:      int64(len(payload)),
		Format: model.AudioFormat{
			Extension:   "flac",
			ContentType: "audio/flac",
			Codec:       "flac",
			BitrateKbps: 1411,
		},
		ETag: `"deterministic-etag"`,
	}

	src := &fakeSource{
		resolveFn: func(ctx context.Context, id model.TrackIdentity, quality source.Quality) (model.ResolvedStream, error) {
			return desc, nil
		},
	}

	proxy, err := NewProxy(src, source.QualityLossless, server.Client(), audioCache)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}

	return &proxyFixture{
		proxy:    proxy,
		source:   src,
		upstream: upFixture,
		payload:  payload,
		trackID:  trackID,
		desc:     desc,
		cache:    audioCache,
	}
}

func TestProxy_FullGET_CacheMissThenCacheHit(t *testing.T) {
	t.Parallel()
	f := setupProxyFixture(t)

	desc, err := f.proxy.Describe(context.Background(), f.trackID)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}

	// 1. First request: cache miss.
	req1 := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
	rec1 := httptest.NewRecorder()
	if err := f.proxy.Serve(rec1, req1, f.trackID, desc); err != nil {
		t.Fatalf("Serve cache miss failed: %v", err)
	}

	if rec1.Code != http.StatusOK {
		t.Fatalf("cache miss status = %d, want %d", rec1.Code, http.StatusOK)
	}
	if rec1.Header().Get("Content-Type") != desc.Format.ContentType {
		t.Fatalf("Content-Type = %q, want %q", rec1.Header().Get("Content-Type"), desc.Format.ContentType)
	}
	if rec1.Header().Get("Content-Length") != strconv.Itoa(len(f.payload)) {
		t.Fatalf("Content-Length = %q, want %d", rec1.Header().Get("Content-Length"), len(f.payload))
	}
	if rec1.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", rec1.Header().Get("Accept-Ranges"))
	}
	if rec1.Header().Get("ETag") != f.proxy.ETag(f.trackID, desc) {
		t.Fatalf("ETag = %q, want %q", rec1.Header().Get("ETag"), f.proxy.ETag(f.trackID, desc))
	}
	if !bytes.Equal(rec1.Body.Bytes(), f.payload) {
		t.Fatalf("cache miss body mismatch: got %d bytes, want %d bytes", rec1.Body.Len(), len(f.payload))
	}
	if hits := f.upstream.requestCount.Load(); hits != 1 {
		t.Fatalf("upstream hits after cache miss = %d, want 1", hits)
	}

	// Verify entry is recorded in cache manager.
	entry, hit, err := f.cache.Lookup(context.Background(), f.proxy.CacheKey(f.trackID, desc))
	if err != nil {
		t.Fatalf("cache Lookup failed: %v", err)
	}
	if !hit || entry.Size != int64(len(f.payload)) {
		t.Fatalf("expected cache hit with size %d, got hit=%v size=%d", len(f.payload), hit, entry.Size)
	}

	// 2. Second request: cache hit.
	req2 := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
	rec2 := httptest.NewRecorder()
	if err := f.proxy.Serve(rec2, req2, f.trackID, desc); err != nil {
		t.Fatalf("Serve cache hit failed: %v", err)
	}

	if rec2.Code != http.StatusOK {
		t.Fatalf("cache hit status = %d, want %d", rec2.Code, http.StatusOK)
	}
	if rec2.Header().Get("Content-Length") != strconv.Itoa(len(f.payload)) {
		t.Fatalf("Content-Length = %q, want %d", rec2.Header().Get("Content-Length"), len(f.payload))
	}
	if !bytes.Equal(rec2.Body.Bytes(), f.payload) {
		t.Fatalf("cache hit body mismatch: got %d bytes, want %d bytes", rec2.Body.Len(), len(f.payload))
	}
	if hits := f.upstream.requestCount.Load(); hits != 1 {
		t.Fatalf("upstream hits after cache hit = %d, want 1 (upstream count must stay one)", hits)
	}
}

func TestProxy_Ranges_206_Uncached(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rangeHeader string
		wantStart   int64
		wantEnd     int64
	}{
		{name: "zero prefix unbounded (0-)", rangeHeader: "bytes=0-", wantStart: 0, wantEnd: 999},
		{name: "zero prefix bounded (0-499)", rangeHeader: "bytes=0-499", wantStart: 0, wantEnd: 499},
		{name: "middle (250-749)", rangeHeader: "bytes=250-749", wantStart: 250, wantEnd: 749},
		{name: "suffix (-200)", rangeHeader: "bytes=-200", wantStart: 800, wantEnd: 999},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := setupProxyFixture(t)

			desc, err := f.proxy.Describe(context.Background(), f.trackID)
			if err != nil {
				t.Fatalf("Describe failed: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
			req.Header.Set("Range", tc.rangeHeader)
			rec := httptest.NewRecorder()

			if err := f.proxy.Serve(rec, req, f.trackID, desc); err != nil {
				t.Fatalf("Serve %s failed: %v", tc.rangeHeader, err)
			}

			if rec.Code != http.StatusPartialContent {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
			}
			wantRange := fmt.Sprintf("bytes %d-%d/%d", tc.wantStart, tc.wantEnd, len(f.payload))
			if got := rec.Header().Get("Content-Range"); got != wantRange {
				t.Fatalf("Content-Range = %q, want %q", got, wantRange)
			}
			wantLen := strconv.FormatInt(tc.wantEnd-tc.wantStart+1, 10)
			if got := rec.Header().Get("Content-Length"); got != wantLen {
				t.Fatalf("Content-Length = %q, want %q", got, wantLen)
			}
			wantBody := f.payload[tc.wantStart : tc.wantEnd+1]
			if !bytes.Equal(rec.Body.Bytes(), wantBody) {
				t.Fatalf("body mismatch for %s: got %d bytes, want %d bytes", tc.rangeHeader, rec.Body.Len(), len(wantBody))
			}
			if hits := f.upstream.requestCount.Load(); hits != 1 {
				t.Fatalf("upstream hits = %d, want 1", hits)
			}
		})
	}
}

func TestProxy_Ranges_206_Cached(t *testing.T) {
	t.Parallel()
	f := setupProxyFixture(t)

	desc, err := f.proxy.Describe(context.Background(), f.trackID)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}

	// Warm cache with full GET.
	reqWarm := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
	recWarm := httptest.NewRecorder()
	if err := f.proxy.Serve(recWarm, reqWarm, f.trackID, desc); err != nil {
		t.Fatalf("Serve cache warm failed: %v", err)
	}
	if recWarm.Code != http.StatusOK {
		t.Fatalf("cache warm status = %d, want %d", recWarm.Code, http.StatusOK)
	}
	if hits := f.upstream.requestCount.Load(); hits != 1 {
		t.Fatalf("upstream hits after cache warm = %d, want 1", hits)
	}

	tests := []struct {
		name        string
		rangeHeader string
		wantStart   int64
		wantEnd     int64
	}{
		{name: "zero prefix unbounded (0-)", rangeHeader: "bytes=0-", wantStart: 0, wantEnd: 999},
		{name: "zero prefix bounded (0-499)", rangeHeader: "bytes=0-499", wantStart: 0, wantEnd: 499},
		{name: "middle (250-749)", rangeHeader: "bytes=250-749", wantStart: 250, wantEnd: 749},
		{name: "suffix (-200)", rangeHeader: "bytes=-200", wantStart: 800, wantEnd: 999},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
			req.Header.Set("Range", tc.rangeHeader)
			rec := httptest.NewRecorder()

			if err := f.proxy.Serve(rec, req, f.trackID, desc); err != nil {
				t.Fatalf("Serve %s failed: %v", tc.rangeHeader, err)
			}

			if rec.Code != http.StatusPartialContent {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
			}
			wantRange := fmt.Sprintf("bytes %d-%d/%d", tc.wantStart, tc.wantEnd, len(f.payload))
			if got := rec.Header().Get("Content-Range"); got != wantRange {
				t.Fatalf("Content-Range = %q, want %q", got, wantRange)
			}
			wantLen := strconv.FormatInt(tc.wantEnd-tc.wantStart+1, 10)
			if got := rec.Header().Get("Content-Length"); got != wantLen {
				t.Fatalf("Content-Length = %q, want %q", got, wantLen)
			}
			wantBody := f.payload[tc.wantStart : tc.wantEnd+1]
			if !bytes.Equal(rec.Body.Bytes(), wantBody) {
				t.Fatalf("body mismatch for %s: got %d bytes, want %d bytes", tc.rangeHeader, rec.Body.Len(), len(wantBody))
			}
			if hits := f.upstream.requestCount.Load(); hits != 1 {
				t.Fatalf("upstream hits = %d, want 1 (upstream count must stay one on cache hit)", hits)
			}
		})
	}
}

func TestProxy_UpstreamIgnoresRange_ReturnsExactRequestedBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rangeHeader string
		wantStart   int64
		wantEnd     int64
	}{
		{name: "zero prefix unbounded (0-)", rangeHeader: "bytes=0-", wantStart: 0, wantEnd: 999},
		{name: "zero prefix bounded (0-499)", rangeHeader: "bytes=0-499", wantStart: 0, wantEnd: 499},
		{name: "middle (250-749)", rangeHeader: "bytes=250-749", wantStart: 250, wantEnd: 749},
		{name: "suffix (-200)", rangeHeader: "bytes=-200", wantStart: 800, wantEnd: 999},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := setupProxyFixture(t)
			f.upstream.ignoreRange.Store(true)

			desc, err := f.proxy.Describe(context.Background(), f.trackID)
			if err != nil {
				t.Fatalf("Describe failed: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
			req.Header.Set("Range", tc.rangeHeader)
			rec := httptest.NewRecorder()

			if err := f.proxy.Serve(rec, req, f.trackID, desc); err != nil {
				t.Fatalf("Serve %s failed: %v", tc.rangeHeader, err)
			}

			// Even though upstream responded 200 OK, proxy must return 206 with exact range.
			if rec.Code != http.StatusPartialContent {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
			}
			wantRange := fmt.Sprintf("bytes %d-%d/%d", tc.wantStart, tc.wantEnd, len(f.payload))
			if got := rec.Header().Get("Content-Range"); got != wantRange {
				t.Fatalf("Content-Range = %q, want %q", got, wantRange)
			}
			wantLen := strconv.FormatInt(tc.wantEnd-tc.wantStart+1, 10)
			if got := rec.Header().Get("Content-Length"); got != wantLen {
				t.Fatalf("Content-Length = %q, want %q", got, wantLen)
			}
			wantBody := f.payload[tc.wantStart : tc.wantEnd+1]
			if !bytes.Equal(rec.Body.Bytes(), wantBody) {
				t.Fatalf("body mismatch for %s: got %d bytes, want %d bytes", tc.rangeHeader, rec.Body.Len(), len(wantBody))
			}
			if hits := f.upstream.requestCount.Load(); hits != 1 {
				t.Fatalf("upstream hits = %d, want 1", hits)
			}
		})
	}
}

func TestProxy_RequestedRangeNotSatisfiable_416(t *testing.T) {
	t.Parallel()

	t.Run("uncached past EOF", func(t *testing.T) {
		t.Parallel()
		f := setupProxyFixture(t)
		desc, err := f.proxy.Describe(context.Background(), f.trackID)
		if err != nil {
			t.Fatalf("Describe failed: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
		req.Header.Set("Range", "bytes=1000-")
		rec := httptest.NewRecorder()

		if err := f.proxy.Serve(rec, req, f.trackID, desc); err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}

		if rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestedRangeNotSatisfiable)
		}
		wantRange := fmt.Sprintf("bytes */%d", len(f.payload))
		if got := rec.Header().Get("Content-Range"); got != wantRange {
			t.Fatalf("Content-Range = %q, want %q", got, wantRange)
		}
		if !strings.Contains(rec.Body.String(), "range not satisfiable") {
			t.Fatalf("unexpected body: %q", rec.Body.String())
		}
		if hits := f.upstream.requestCount.Load(); hits != 0 {
			t.Fatalf("upstream hits = %d, want 0 (proxy must reject 416 before calling upstream)", hits)
		}
	})

	t.Run("uncached out of bounds", func(t *testing.T) {
		t.Parallel()
		f := setupProxyFixture(t)
		desc, err := f.proxy.Describe(context.Background(), f.trackID)
		if err != nil {
			t.Fatalf("Describe failed: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
		req.Header.Set("Range", "bytes=2000-3000")
		rec := httptest.NewRecorder()

		if err := f.proxy.Serve(rec, req, f.trackID, desc); err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}

		if rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestedRangeNotSatisfiable)
		}
		wantRange := fmt.Sprintf("bytes */%d", len(f.payload))
		if got := rec.Header().Get("Content-Range"); got != wantRange {
			t.Fatalf("Content-Range = %q, want %q", got, wantRange)
		}
		if hits := f.upstream.requestCount.Load(); hits != 0 {
			t.Fatalf("upstream hits = %d, want 0", hits)
		}
	})

	t.Run("cached past EOF", func(t *testing.T) {
		t.Parallel()
		f := setupProxyFixture(t)
		desc, err := f.proxy.Describe(context.Background(), f.trackID)
		if err != nil {
			t.Fatalf("Describe failed: %v", err)
		}

		// Warm cache
		reqWarm := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
		recWarm := httptest.NewRecorder()
		if err := f.proxy.Serve(recWarm, reqWarm, f.trackID, desc); err != nil {
			t.Fatalf("Serve cache warm failed: %v", err)
		}
		if recWarm.Code != http.StatusOK {
			t.Fatalf("cache warm status = %d, want %d", recWarm.Code, http.StatusOK)
		}
		if hits := f.upstream.requestCount.Load(); hits != 1 {
			t.Fatalf("upstream hits after warm = %d, want 1", hits)
		}

		// Send unsatisfiable range on cached entry
		req := httptest.NewRequest(http.MethodGet, "/track.flac", nil)
		req.Header.Set("Range", "bytes=1000-")
		rec := httptest.NewRecorder()

		if err := f.proxy.Serve(rec, req, f.trackID, desc); err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}

		if rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestedRangeNotSatisfiable)
		}
		wantRange := fmt.Sprintf("bytes */%d", len(f.payload))
		if got := rec.Header().Get("Content-Range"); got != wantRange {
			t.Fatalf("Content-Range = %q, want %q", got, wantRange)
		}
		if hits := f.upstream.requestCount.Load(); hits != 1 {
			t.Fatalf("upstream hits = %d, want 1", hits)
		}
	})
}
