package stream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xtawa/tunebridge/internal/cache"
	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
	"golang.org/x/sync/singleflight"
)

// Proxy is the only component allowed to follow provider-owned audio URLs. It
// never redirects a WebDAV client to an upstream signed URL.
type Proxy struct {
	source  source.MusicSource
	quality source.Quality
	http    *http.Client
	cache   *cache.Manager

	mu          sync.Mutex
	descriptors map[string]cachedDescriptor
	resolve     singleflight.Group
	fillMu      sync.Mutex
	fills       map[string]chan struct{}
}

type cachedDescriptor struct {
	stream model.ResolvedStream
	until  time.Time
}

func NewProxy(src source.MusicSource, quality source.Quality, client *http.Client, audioCache *cache.Manager) (*Proxy, error) {
	if src == nil || audioCache == nil {
		return nil, errors.New("stream proxy requires source and audio cache")
	}
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second}}
	}
	return &Proxy{source: src, quality: quality, http: client, cache: audioCache, descriptors: make(map[string]cachedDescriptor), fills: make(map[string]chan struct{})}, nil
}

func (p *Proxy) activePopulation(key string) (chan struct{}, bool) {
	p.fillMu.Lock()
	defer p.fillMu.Unlock()
	done, ok := p.fills[key]
	return done, ok
}

func (p *Proxy) startPopulation(key string) (chan struct{}, bool) {
	p.fillMu.Lock()
	defer p.fillMu.Unlock()
	if done, ok := p.fills[key]; ok {
		return done, true
	}
	done := make(chan struct{})
	p.fills[key] = done
	return done, false
}

func (p *Proxy) completePopulation(key string, done chan struct{}) {
	p.fillMu.Lock()
	if p.fills[key] == done {
		delete(p.fills, key)
		close(done)
	}
	p.fillMu.Unlock()
}

func (p *Proxy) Describe(ctx context.Context, identity model.TrackIdentity) (model.ResolvedStream, error) {
	key := identity.String() + ":" + string(p.quality)
	p.mu.Lock()
	cached, ok := p.descriptors[key]
	p.mu.Unlock()
	if ok && time.Now().Before(cached.until) {
		return cached.stream, nil
	}
	value, err, _ := p.resolve.Do(key, func() (any, error) {
		p.mu.Lock()
		current, currentOK := p.descriptors[key]
		p.mu.Unlock()
		if currentOK && time.Now().Before(current.until) {
			return current.stream, nil
		}
		resolved, err := p.source.ResolveStream(ctx, identity, p.quality)
		if err != nil {
			return model.ResolvedStream{}, err
		}
		if err := validateStream(resolved); err != nil {
			return model.ResolvedStream{}, err
		}
		until := time.Now().Add(2 * time.Minute)
		if !resolved.ExpiresAt.IsZero() && resolved.ExpiresAt.Before(until) {
			until = resolved.ExpiresAt.Add(-15 * time.Second)
		}
		p.mu.Lock()
		p.descriptors[key] = cachedDescriptor{stream: resolved, until: until}
		p.mu.Unlock()
		return resolved, nil
	})
	if err != nil {
		return model.ResolvedStream{}, err
	}
	return value.(model.ResolvedStream), nil
}

func validateStream(stream model.ResolvedStream) error {
	parsed, err := url.Parse(stream.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return errors.New("source returned unsafe audio URL")
	}
	if stream.Size < 0 {
		return errors.New("source returned audio stream without a content length")
	}
	if stream.Format.ContentType == "" || stream.Format.Codec == "" || stream.Format.Extension == "" {
		return errors.New("source returned incomplete audio representation")
	}
	return nil
}

func (p *Proxy) ETag(id model.TrackIdentity, desc model.ResolvedStream) string {
	// An upstream ETag is only one input: it may be absent, malformed, or scoped
	// to a CDN URL. The exposed validator is always a quoted hash over the
	// complete representation identity we can prove locally.
	hash := sha256.Sum256([]byte(id.String() + "\x00" + string(p.quality) + "\x00" + desc.Format.Codec + "\x00" + desc.Format.ContentType + "\x00" + desc.Format.Extension + "\x00" + strconv.FormatInt(desc.Size, 10) + "\x00" + desc.ETag))
	return `"` + hex.EncodeToString(hash[:16]) + `"`
}

func (p *Proxy) CacheKey(id model.TrackIdentity, desc model.ResolvedStream) string {
	return id.String() + ":" + string(p.quality) + ":" + strings.ToLower(desc.Format.Codec)
}

// Serve writes an exact frozen representation. A retry is permitted only when
// the refreshed descriptor is byte-layout equivalent to the first descriptor.
func (p *Proxy) Serve(w http.ResponseWriter, r *http.Request, id model.TrackIdentity, desc model.ResolvedStream) error {
	key := p.CacheKey(id, desc)
	var requested *ByteRange
	if raw := r.Header.Get("Range"); raw != "" {
		parsed, err := ParseRange(raw, desc.Size)
		if err != nil {
			return rangeError(w, desc.Size, err)
		}
		requested = &parsed
	}
	for {
		if entry, hit, err := p.cache.Lookup(r.Context(), key); err != nil {
			return err
		} else if hit {
			defer p.cache.Lease(key)()
			return serveFile(w, r, entry.Path, desc, p.ETag(id, desc))
		}
		if done, alreadyFilling := p.activePopulation(key); alreadyFilling {
			select {
			case <-r.Context().Done():
				return r.Context().Err()
			case <-done:
				continue // a completed fill may now be safely served from disk.
			}
		}
		break
	}
	start, end := int64(0), desc.Size-1
	if requested != nil {
		start, end = requested.Start, requested.End
	}
	cacheAll := requested == nil && start == 0
	if cacheAll {
		done, alreadyFilling := p.startPopulation(key)
		if alreadyFilling {
			// No response has been committed yet, so a waiter can safely retry
			// against the completed on-disk object.
			select {
			case <-r.Context().Done():
				return r.Context().Err()
			case <-done:
				return p.Serve(w, r, id, desc)
			}
		}
		defer p.completePopulation(key, done)
	}

	response, active, err := p.openWithRefresh(r.Context(), id, desc, start, end)
	if err != nil {
		return fmt.Errorf("open upstream audio: %w", err)
	}
	desc = active
	setHeaders(w, desc, p.ETag(id, desc), start, end, requested != nil)
	partialPath := ""
	var partial *os.File
	if cacheAll {
		partial, partialPath, err = p.cache.NewPartial()
		if err != nil {
			return err
		}
		defer func() {
			if partialPath != "" {
				_ = os.Remove(partialPath)
			}
		}()
	}
	var written int64
	copyErr := error(nil)
	for retry := 0; retry < 3 && written < end-start+1; retry++ {
		if retry > 0 {
			// A premature body EOF/reset is retryable only through a fresh provider
			// resolution. Reusing a stale signed URL can repeatedly truncate the
			// response and never exercises the provider's URL refresh contract.
			refreshed, refreshErr := p.refresh(r.Context(), id, desc)
			if refreshErr != nil {
				copyErr = refreshErr
				break
			}
			response, active, err = p.openWithRefresh(r.Context(), id, refreshed, start+written, end)
			if err != nil {
				copyErr = err
				break
			}
			// A response already started with desc; a refreshed representation must
			// be equivalent or the only safe choice is to terminate the response.
			if !equivalent(desc, active) {
				response.Body.Close()
				copyErr = errors.New("refreshed audio representation differs from active response")
				break
			}
		}
		var cacheWriter io.Writer
		if partial != nil {
			cacheWriter = partial
		}
		n, err := copyUpstream(w, cacheWriter, response, start+written, end)
		response.Body.Close()
		written += n
		if err == nil {
			copyErr = nil
			break
		}
		copyErr = err
		if r.Context().Err() != nil {
			break
		} // client disconnected: immediately stop upstream/cache work.
	}
	if partial != nil {
		if syncErr := partial.Sync(); copyErr == nil {
			copyErr = syncErr
		}
		if closeErr := partial.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr == nil && written == desc.Size {
			if _, err := p.cache.Commit(r.Context(), partialPath, p.CacheKey(id, desc), desc.Format.ContentType, desc.Format.Codec, string(p.quality), desc.Size); err != nil {
				return err
			}
			partialPath = ""
		}
	}
	if written != end-start+1 && copyErr == nil {
		copyErr = io.ErrUnexpectedEOF
	}
	return copyErr
}

func (p *Proxy) openWithRefresh(ctx context.Context, id model.TrackIdentity, original model.ResolvedStream, start, end int64) (*http.Response, model.ResolvedStream, error) {
	response, err := p.open(ctx, original, start, end)
	if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, original, nil
	}
	if response != nil {
		response.Body.Close()
	}
	if err == nil && response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden {
		return nil, original, fmt.Errorf("upstream audio status %d", response.StatusCode)
	}
	refreshed, refreshErr := p.refresh(ctx, id, original)
	if refreshErr != nil {
		if err != nil {
			return nil, original, err
		}
		return nil, original, refreshErr
	}
	response, err = p.open(ctx, refreshed, start, end)
	if err != nil {
		return nil, original, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, original, fmt.Errorf("upstream audio status %d", response.StatusCode)
	}
	return response, refreshed, nil
}

func (p *Proxy) refresh(ctx context.Context, id model.TrackIdentity, original model.ResolvedStream) (model.ResolvedStream, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return model.ResolvedStream{}, ctx.Err()
			case <-time.After(time.Duration(50*(1<<attempt)) * time.Millisecond):
			}
		}
		p.mu.Lock()
		delete(p.descriptors, id.String()+":"+string(p.quality))
		p.mu.Unlock()
		desc, err := p.Describe(ctx, id)
		if err == nil && equivalent(original, desc) {
			return desc, nil
		}
		if err == nil {
			return model.ResolvedStream{}, errors.New("refreshed audio representation differs from active response")
		}
		last = err
	}
	return model.ResolvedStream{}, last
}

func equivalent(a, b model.ResolvedStream) bool {
	return a.Size == b.Size && a.Format.Codec == b.Format.Codec && a.Format.ContentType == b.Format.ContentType && a.Format.Extension == b.Format.Extension
}

func (p *Proxy) open(ctx context.Context, desc model.ResolvedStream, start, end int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, desc.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	return p.http.Do(req)
}

func copyUpstream(w io.Writer, cacheWriter io.Writer, response *http.Response, start, end int64) (int64, error) {
	if response.StatusCode == http.StatusPartialContent {
		if !strings.HasPrefix(response.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", start)) {
			return 0, errors.New("upstream returned a mismatched content range")
		}
		return copyExact(w, cacheWriter, response.Body, end-start+1)
	}
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected upstream status %d", response.StatusCode)
	}
	if start > 0 {
		if _, err := io.CopyN(io.Discard, response.Body, start); err != nil {
			return 0, fmt.Errorf("upstream ignored Range but is too short: %w", err)
		}
	}
	return copyExact(w, cacheWriter, response.Body, end-start+1)
}

func copyExact(client io.Writer, cacheWriter io.Writer, source io.Reader, expected int64) (int64, error) {
	var dst io.Writer = client
	if cacheWriter != nil {
		dst = io.MultiWriter(client, cacheWriter)
	}
	written, err := io.CopyN(dst, source, expected)
	if err != nil {
		return written, fmt.Errorf("stream upstream audio: %w", err)
	}
	return written, nil
}

func setHeaders(w http.ResponseWriter, desc model.ResolvedStream, etag string, start, end int64, partial bool) {
	w.Header().Set("Content-Type", desc.Format.ContentType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", etag)
	if !desc.LastModified.IsZero() {
		w.Header().Set("Last-Modified", desc.LastModified.UTC().Format(http.TimeFormat))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, desc.Size))
		w.WriteHeader(http.StatusPartialContent)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func rangeError(w http.ResponseWriter, size int64, err error) error {
	if errors.Is(err, ErrUnsatisfiableRange) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return nil
	}
	http.Error(w, "invalid Range header", http.StatusRequestedRangeNotSatisfiable)
	return nil
}

func serveFile(w http.ResponseWriter, r *http.Request, filename string, desc model.ResolvedStream, etag string) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	var requested *ByteRange
	if raw := r.Header.Get("Range"); raw != "" {
		parsed, err := ParseRange(raw, desc.Size)
		if err != nil {
			return rangeError(w, desc.Size, err)
		}
		requested = &parsed
	}
	start, end := int64(0), desc.Size-1
	if requested != nil {
		start, end = requested.Start, requested.End
	}
	setHeaders(w, desc, etag, start, end, requested != nil)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	_, err = io.CopyN(w, f, end-start+1)
	return err
}
