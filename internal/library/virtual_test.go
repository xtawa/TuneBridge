package library

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/xtawa/tunebridge/internal/cache"
	"github.com/xtawa/tunebridge/internal/database"
	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
	"github.com/xtawa/tunebridge/internal/source/netease"
	"github.com/xtawa/tunebridge/internal/stream"
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
