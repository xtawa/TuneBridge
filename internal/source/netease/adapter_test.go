package netease

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
)

func TestAdapterResolvesActualStreamFormatWithStoredSession(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/song/url/v1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Cookie") != "MUSIC_U=private" {
			t.Fatalf("missing session cookie")
		}
		if r.URL.Query().Get("id") != "186016" || r.URL.Query().Get("level") != "lossless" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		writeJSON(w, `{"code":200,"data":[{"url":"https://cdn.example.test/audio","size":12345,"type":"mp3","encodeType":"mp3","br":320000}]}`)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter(client, staticSession{payload: []byte("MUSIC_U=private")})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.ResolveStream(context.Background(), model.TrackIdentity{SourceID: SourceID, TrackID: "186016"}, source.QualityLossless)
	if err != nil {
		t.Fatal(err)
	}
	if stream.Size != 12345 || stream.Format.Extension != "mp3" || stream.Format.ContentType != "audio/mpeg" || stream.Format.BitrateKbps != 320 {
		t.Fatalf("unexpected stream: %#v", stream)
	}
}

func TestAdapterLyricsPreservesAllSupportedVariants(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"code":200,"lrc":{"lyric":"original"},"tlyric":{"lyric":"translated"},"romalrc":{"lyric":"romanized"}}`)
	}))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	adapter, _ := NewAdapter(client, staticSession{payload: []byte("MUSIC_U=private")})
	lyrics, err := adapter.Lyrics(context.Background(), model.TrackIdentity{SourceID: SourceID, TrackID: "186016"})
	if err != nil || lyrics.Original != "original" || lyrics.Translated != "translated" || lyrics.Romanized != "romanized" {
		t.Fatalf("lyrics=%#v err=%v", lyrics, err)
	}
}

func TestAdapterRejectsCallsWithoutPersistedSession(t *testing.T) {
	t.Parallel()
	client, _ := New("http://example.test", nil)
	adapter, _ := NewAdapter(client, staticSession{err: errors.New("missing")})
	_, err := adapter.UserProfile(context.Background())
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err=%v", err)
	}
}

func TestPlaylistFetchesMetadataThenTrackPages(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/playlist/detail":
			writeJSON(w, `{"code":200,"playlist":{"id":9,"name":"测试歌单","description":"desc","trackCount":1}}`)
		case "/playlist/track/all":
			if r.URL.Query().Get("limit") != "500" || r.URL.Query().Get("offset") != "0" {
				t.Fatalf("pagination query=%s", r.URL.RawQuery)
			}
			writeJSON(w, `{"code":200,"songs":[{"id":186016,"name":"晴天","ar":[{"name":"周杰伦"}],"al":{"id":1,"name":"叶惠美","picUrl":"https://cover.example.test/a.jpg"}}]}`)
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	adapter, _ := NewAdapter(client, staticSession{payload: []byte("MUSIC_U=private")})
	playlist, tracks, err := adapter.Playlist(context.Background(), "9")
	if err != nil || playlist.Name != "测试歌单" || len(tracks) != 1 || tracks[0].Identity.TrackID != "186016" || requests != 2 {
		t.Fatalf("playlist=%#v tracks=%#v requests=%d err=%v", playlist, tracks, requests, err)
	}
}

func TestTracksFromDTO_ExtractsQualityAndEstimatedSize(t *testing.T) {
	t.Parallel()

	items := []songDTO{
		{
			ID:   "1",
			Name: "Lossless Track",
			SQ:   &audioQualityDTO{Bitrate: 960000, Size: 32000000},
			H:    &audioQualityDTO{Bitrate: 320000, Size: 10000000},
		},
		{
			ID:   "2",
			Name: "High MP3 Track",
			H:    &audioQualityDTO{Bitrate: 320000, Size: 9500000},
		},
		{
			ID:       "3",
			Name:     "Fallback Track",
			Duration: 200000,
		},
	}

	tracks := tracksFromDTO(items)
	if len(tracks) != 3 {
		t.Fatalf("expected 3 tracks, got %d", len(tracks))
	}

	// Track 1: has SQ, so flac and 32MB
	if tracks[0].EstimatedFormat.Extension != "flac" || tracks[0].EstimatedSize != 32000000 {
		t.Fatalf("track 0 unexpected: format=%+v size=%d", tracks[0].EstimatedFormat, tracks[0].EstimatedSize)
	}

	// Track 2: only H, so mp3 and 9.5MB
	if tracks[1].EstimatedFormat.Extension != "mp3" || tracks[1].EstimatedSize != 9500000 {
		t.Fatalf("track 1 unexpected: format=%+v size=%d", tracks[1].EstimatedFormat, tracks[1].EstimatedSize)
	}

	// Track 3: fallback by duration
	if tracks[2].EstimatedFormat.Extension != "mp3" || tracks[2].EstimatedSize <= 0 {
		t.Fatalf("track 2 unexpected: format=%+v size=%d", tracks[2].EstimatedFormat, tracks[2].EstimatedSize)
	}
}

type staticSession struct {
	payload []byte
	err     error
}

func (s staticSession) Load(context.Context, string) (source.Session, error) {
	return source.Session{Payload: s.payload}, s.err
}
