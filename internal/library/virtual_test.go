package library

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtawa/tunebridge/internal/cache"
	"github.com/xtawa/tunebridge/internal/database"
	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
	"github.com/xtawa/tunebridge/internal/source/netease"
	"github.com/xtawa/tunebridge/internal/stream"
	"github.com/xtawa/tunebridge/internal/webdav"
)

type unauthedSource struct{}

func (u unauthedSource) ID() string { return "netease" }
func (u unauthedSource) Authenticate(context.Context, source.AuthInput) (source.Session, error) {
	return source.Session{}, netease.ErrUnauthenticated
}
func (u unauthedSource) RefreshSession(context.Context, source.Session) (source.Session, error) {
	return source.Session{}, netease.ErrUnauthenticated
}
func (u unauthedSource) UserProfile(context.Context) (source.UserProfile, error) {
	return source.UserProfile{}, netease.ErrUnauthenticated
}
func (u unauthedSource) LikedTracks(context.Context) ([]model.Track, error) {
	return nil, netease.ErrUnauthenticated
}
func (u unauthedSource) Playlists(context.Context) ([]source.Playlist, error) {
	return nil, netease.ErrUnauthenticated
}
func (u unauthedSource) Playlist(context.Context, string) (source.Playlist, []model.Track, error) {
	return source.Playlist{}, nil, netease.ErrUnauthenticated
}
func (u unauthedSource) DailyRecommendations(context.Context) ([]model.Track, error) {
	return nil, netease.ErrUnauthenticated
}
func (u unauthedSource) SearchTracks(context.Context, string) ([]model.Track, error) {
	return nil, netease.ErrUnauthenticated
}
func (u unauthedSource) Track(context.Context, model.TrackIdentity) (model.Track, error) {
	return model.Track{}, netease.ErrUnauthenticated
}
func (u unauthedSource) Cover(context.Context, model.TrackIdentity) (model.Cover, error) {
	return model.Cover{}, netease.ErrUnauthenticated
}
func (u unauthedSource) Lyrics(context.Context, model.TrackIdentity) (model.Lyrics, error) {
	return model.Lyrics{}, netease.ErrUnauthenticated
}
func (u unauthedSource) ResolveStream(context.Context, model.TrackIdentity, source.Quality) (model.ResolvedStream, error) {
	return model.ResolvedStream{}, netease.ErrUnauthenticated
}

func TestVirtualLibrary_UnauthenticatedReturnsEmptyListWithoutError(t *testing.T) {
	t.Parallel()
	src := unauthedSource{}

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

	proxy, err := stream.NewProxy(src, source.QualityStandard, http.DefaultClient, audioCache)
	if err != nil {
		t.Fatal(err)
	}

	vl, err := NewVirtualLibrary(src, proxy, nil, time.Minute, time.Minute, "original")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Liked tracks: unauthenticated should return empty slice, NOT an error
	likedItems, err := vl.List(context.Background(), likedRoot)
	if err != nil {
		t.Fatalf("expected nil error for unauthenticated likedRoot, got: %v", err)
	}
	if len(likedItems) != 0 {
		t.Fatalf("expected 0 items, got: %d", len(likedItems))
	}

	// 2. Playlists: unauthenticated should return empty slice, NOT an error
	playlistsItems, err := vl.List(context.Background(), playlistsRoot)
	if err != nil {
		t.Fatalf("expected nil error for unauthenticated playlistsRoot, got: %v", err)
	}
	if len(playlistsItems) != 0 {
		t.Fatalf("expected 0 items, got: %d", len(playlistsItems))
	}

	// 3. Daily recommendations: unauthenticated should return empty slice, NOT an error
	dailyItems, err := vl.List(context.Background(), dailyRoot)
	if err != nil {
		t.Fatalf("expected nil error for unauthenticated dailyRoot, got: %v", err)
	}
	if len(dailyItems) != 0 {
		t.Fatalf("expected 0 items, got: %d", len(dailyItems))
	}
}

type fakeTrackSource struct {
	unauthedSource
	likedTracksFn    func(ctx context.Context) ([]model.Track, error)
	resolveStreamFn  func(ctx context.Context, id model.TrackIdentity, quality source.Quality) (model.ResolvedStream, error)
	resolveCallCount atomic.Int64
	likedCallCount   atomic.Int64
}

func (f *fakeTrackSource) LikedTracks(ctx context.Context) ([]model.Track, error) {
	f.likedCallCount.Add(1)
	if f.likedTracksFn != nil {
		return f.likedTracksFn(ctx)
	}
	return nil, nil
}

func (f *fakeTrackSource) ResolveStream(ctx context.Context, id model.TrackIdentity, quality source.Quality) (model.ResolvedStream, error) {
	f.resolveCallCount.Add(1)
	if f.resolveStreamFn != nil {
		return f.resolveStreamFn(ctx, id, quality)
	}
	return model.ResolvedStream{
		URL:       "https://example.test/stream.flac",
		Size:      1234567,
		Format:    model.AudioFormat{Extension: "flac", ContentType: "audio/flac", Codec: "flac", BitrateKbps: 900},
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

func TestVirtualLibrary_LazyStreamResolution_ZeroCallsOnList(t *testing.T) {
	t.Parallel()

	// Prepare 50 tracks
	tracks := make([]model.Track, 50)
	for i := 0; i < 50; i++ {
		tracks[i] = model.Track{
			Identity:        model.TrackIdentity{SourceID: "netease", TrackID: fmt.Sprintf("%d", 1000+i)},
			Title:           fmt.Sprintf("Song %d", i),
			Artists:         []string{"Artist"},
			EstimatedFormat: model.AudioFormat{Extension: "flac", ContentType: "audio/flac", Codec: "flac", BitrateKbps: 900},
			EstimatedSize:   30000000 + int64(i*1000),
		}
	}

	src := &fakeTrackSource{
		likedTracksFn: func(ctx context.Context) ([]model.Track, error) {
			return tracks, nil
		},
	}

	dbPath := filepath.Join(t.TempDir(), "tunebridge.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("database.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cacheDir := t.TempDir()
	audioCache, err := cache.NewAudioManager(db, cacheDir, 10*1024*1024)
	if err != nil {
		t.Fatalf("cache.NewAudioManager failed: %v", err)
	}

	proxy, err := stream.NewProxy(src, source.QualityLossless, http.DefaultClient, audioCache)
	if err != nil {
		t.Fatal(err)
	}

	vl, err := NewVirtualLibrary(src, proxy, nil, time.Minute, time.Minute, "original")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Calling List MUST NOT trigger ResolveStream (it must be 100% lazy)
	items, err := vl.List(context.Background(), likedRoot)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	// 50 audio tracks + 50 lyrics files = 100 items
	if len(items) != 100 {
		t.Fatalf("expected 100 items, got: %d", len(items))
	}

	if calls := src.resolveCallCount.Load(); calls != 0 {
		t.Fatalf("expected 0 ResolveStream calls during List, got: %d", calls)
	}

	// Verify the items have correct extension and size
	var firstAudio *webdav.Resource
	for _, item := range items {
		if item.Kind == webdav.AudioFile {
			if item.Representation != nil {
				t.Fatalf("expected nil Representation before Head/Get, got: %#v", item.Representation)
			}
			if item.Size <= 0 {
				t.Fatalf("expected positive estimated size, got: %d", item.Size)
			}
			if firstAudio == nil {
				cp := item
				firstAudio = &cp
			}
		}
	}

	if firstAudio == nil {
		t.Fatal("no audio resource found")
	}

	// 2. Calling Head on the audio resource triggers on-demand ResolveStream
	described, err := vl.Head(context.Background(), *firstAudio)
	if err != nil {
		t.Fatalf("Head failed: %v", err)
	}

	if described.Representation == nil {
		t.Fatal("expected non-nil Representation after Head")
	}

	if calls := src.resolveCallCount.Load(); calls != 1 {
		t.Fatalf("expected exactly 1 ResolveStream call after Head, got: %d", calls)
	}
}

func TestVirtualLibrary_SingleflightSnapshot_ConcurrentProbes(t *testing.T) {
	t.Parallel()

	src := &fakeTrackSource{
		likedTracksFn: func(ctx context.Context) ([]model.Track, error) {
			time.Sleep(30 * time.Millisecond) // simulate upstream network latency
			return []model.Track{
				{
					Identity:        model.TrackIdentity{SourceID: "netease", TrackID: "1"},
					Title:           "Concurrent Track",
					Artists:         []string{"Test"},
					EstimatedFormat: model.AudioFormat{Extension: "mp3", ContentType: "audio/mpeg", Codec: "mp3", BitrateKbps: 320},
					EstimatedSize:   10000000,
				},
			}, nil
		},
	}

	dbPath := filepath.Join(t.TempDir(), "tunebridge.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("database.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cacheDir := t.TempDir()
	audioCache, err := cache.NewAudioManager(db, cacheDir, 10*1024*1024)
	if err != nil {
		t.Fatalf("cache.NewAudioManager failed: %v", err)
	}

	proxy, err := stream.NewProxy(src, source.QualityStandard, http.DefaultClient, audioCache)
	if err != nil {
		t.Fatal(err)
	}

	vl, err := NewVirtualLibrary(src, proxy, nil, time.Minute, time.Minute, "original")
	if err != nil {
		t.Fatal(err)
	}

	const concurrentWorkers = 10
	var wg sync.WaitGroup
	errCh := make(chan error, concurrentWorkers)

	for i := 0; i < concurrentWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := vl.List(context.Background(), likedRoot)
			if err != nil {
				errCh <- err
				return
			}
			if len(items) != 2 { // 1 audio + 1 lrc
				errCh <- fmt.Errorf("expected 2 items, got %d", len(items))
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("worker error: %v", err)
		}
	}

	// Singleflight guarantees LikedTracks was called only once despite 10 concurrent requests
	if calls := src.likedCallCount.Load(); calls != 1 {
		t.Fatalf("expected exactly 1 LikedTracks call with singleflight, got: %d", calls)
	}
}
