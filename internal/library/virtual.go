package library

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
	"github.com/xtawa/tunebridge/internal/stream"
	"github.com/xtawa/tunebridge/internal/webdav"
)

const (
	neteaseRoot   = "/网易云"
	likedRoot     = neteaseRoot + "/我喜欢的音乐"
	playlistsRoot = neteaseRoot + "/我的歌单"
	dailyRoot     = neteaseRoot + "/每日推荐"
	searchRoot    = neteaseRoot + "/搜索结果"
)

// VirtualLibrary turns provider metadata into a read-only, stable DAV tree.
// Snapshot keys are category identities rather than display names; a path is
// just a generated view and never a database/source identity.
type VirtualLibrary struct {
	source                source.MusicSource
	proxy                 *stream.Proxy
	search                SearchResults
	playlistTTL, dailyTTL time.Duration
	lyricsMode            string

	mu        sync.Mutex
	snapshots map[string]snapshot
}

type SearchResults interface {
	List(ctx context.Context, sourceID string) ([]model.TrackIdentity, error)
}

type snapshot struct {
	expires   time.Time
	resources map[string]webdav.Resource
	children  map[string][]webdav.Resource
}

func NewVirtualLibrary(src source.MusicSource, proxy *stream.Proxy, searchResults SearchResults, playlistTTL, dailyTTL time.Duration, lyricsMode string) (*VirtualLibrary, error) {
	if src == nil || proxy == nil {
		return nil, errors.New("virtual library requires source and stream proxy")
	}
	if lyricsMode == "" {
		lyricsMode = "original_translation"
	}
	if lyricsMode != "original" && lyricsMode != "original_translation" && lyricsMode != "original_romanized" {
		return nil, errors.New("unsupported lyrics mode")
	}
	return &VirtualLibrary{source: src, proxy: proxy, search: searchResults, playlistTTL: playlistTTL, dailyTTL: dailyTTL, lyricsMode: lyricsMode, snapshots: make(map[string]snapshot)}, nil
}

func (l *VirtualLibrary) Stat(ctx context.Context, resourcePath string) (webdav.Resource, error) {
	resourcePath = cleanPath(resourcePath)
	if resourcePath == "/" || resourcePath == neteaseRoot || resourcePath == likedRoot || resourcePath == playlistsRoot || resourcePath == dailyRoot || resourcePath == searchRoot {
		return collection(resourcePath), nil
	}
	if strings.HasPrefix(resourcePath, playlistsRoot+"/") {
		parent := path.Dir(resourcePath)
		if parent == playlistsRoot { // playlist collection must be selected from current list
			s, err := l.playlistIndex(ctx)
			if err != nil {
				return webdav.Resource{}, err
			}
			resource, ok := s.resources[resourcePath]
			if !ok {
				return webdav.Resource{}, webdav.ErrNotFound
			}
			return resource, nil
		}
		playlistPath := parent
		if strings.HasSuffix(resourcePath, ".lrc") {
			playlistPath = path.Dir(resourcePath)
		}
		if s, err := l.playlistSnapshot(ctx, playlistPath); err == nil {
			if resource, ok := s.resources[resourcePath]; ok {
				return resource, nil
			} else {
				return webdav.Resource{}, webdav.ErrNotFound
			}
		} else {
			return webdav.Resource{}, err
		}
	}
	for _, root := range []string{likedRoot, dailyRoot, searchRoot} {
		if strings.HasPrefix(resourcePath, root+"/") {
			s, err := l.trackSnapshot(ctx, root)
			if err != nil {
				return webdav.Resource{}, err
			}
			resource, ok := s.resources[resourcePath]
			if !ok {
				return webdav.Resource{}, webdav.ErrNotFound
			}
			return resource, nil
		}
	}
	return webdav.Resource{}, webdav.ErrNotFound
}

func (l *VirtualLibrary) List(ctx context.Context, resourcePath string) ([]webdav.Resource, error) {
	resourcePath = cleanPath(resourcePath)
	switch resourcePath {
	case "/":
		return []webdav.Resource{collection(neteaseRoot)}, nil
	case neteaseRoot:
		return []webdav.Resource{collection(likedRoot), collection(playlistsRoot), collection(dailyRoot), collection(searchRoot)}, nil
	case playlistsRoot:
		s, err := l.playlistIndex(ctx)
		if err != nil {
			return nil, err
		}
		return cloneResources(s.children[playlistsRoot]), nil
	case likedRoot, dailyRoot, searchRoot:
		s, err := l.trackSnapshot(ctx, resourcePath)
		if err != nil {
			return nil, err
		}
		return cloneResources(s.children[resourcePath]), nil
	default:
		if strings.HasPrefix(resourcePath, playlistsRoot+"/") {
			s, err := l.playlistSnapshot(ctx, resourcePath)
			if err != nil {
				return nil, err
			}
			return cloneResources(s.children[resourcePath]), nil
		}
	}
	return nil, webdav.ErrNotFound
}

func (l *VirtualLibrary) Head(ctx context.Context, resource webdav.Resource) (webdav.Resource, error) {
	if resource.Kind == webdav.AudioFile && resource.Representation == nil {
		desc, err := l.proxy.Describe(ctx, resource.Track)
		if err != nil {
			return resource, err
		}
		resource = l.audioResource(resource.Path, resource.Track, desc)
	}
	return resource, nil
}

func (l *VirtualLibrary) Get(ctx context.Context, w http.ResponseWriter, r *http.Request, resource webdav.Resource) error {
	switch resource.Kind {
	case webdav.AudioFile:
		resource, err := l.Head(ctx, resource)
		if err != nil {
			return err
		}
		return l.proxy.Serve(w, r, resource.Track, *resource.Representation)
	case webdav.LyricsFile:
		lyrics, err := l.source.Lyrics(ctx, resource.Track)
		if err != nil {
			return err
		}
		body := selectLyrics(lyrics, l.lyricsMode)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", fmt.Sprint(len([]byte(body))))
		w.Header().Set("Cache-Control", "private, max-age=300")
		_, err = w.Write([]byte(body))
		return err
	default:
		return errors.New("unsupported virtual file")
	}
}

func (l *VirtualLibrary) playlistIndex(ctx context.Context) (snapshot, error) {
	const key = "playlists"
	if s, ok := l.snapshot(key); ok {
		return s, nil
	}
	items, err := l.source.Playlists(ctx)
	if err != nil {
		return snapshot{}, err
	}
	s := emptySnapshot()
	s.resources[playlistsRoot] = collection(playlistsRoot)
	used := make(map[string]bool)
	for _, item := range items {
		name := uniqueDirectoryName(item.Name, item.ID, used)
		entry := collection(playlistsRoot + "/" + name)
		s.resources[entry.Path] = entry
		s.children[playlistsRoot] = append(s.children[playlistsRoot], entry)
	}
	sortResources(s.children[playlistsRoot])
	l.store(key, s, l.playlistTTL)
	return s, nil
}

func (l *VirtualLibrary) playlistSnapshot(ctx context.Context, directory string) (snapshot, error) {
	index, err := l.playlistIndex(ctx)
	if err != nil {
		return snapshot{}, err
	}
	entry, ok := index.resources[directory]
	if !ok || !entry.IsCollection() {
		return snapshot{}, webdav.ErrNotFound
	}
	key := "playlist:" + directory
	if s, ok := l.snapshot(key); ok {
		return s, nil
	}
	// Names have an ID suffix only on a collision. Resolve the exact source
	// playlist by comparing the generated directory names in current metadata.
	items, err := l.source.Playlists(ctx)
	if err != nil {
		return snapshot{}, err
	}
	used := make(map[string]bool)
	playlistID := ""
	for _, item := range items {
		if playlistsRoot+"/"+uniqueDirectoryName(item.Name, item.ID, used) == directory {
			playlistID = item.ID
			break
		}
	}
	if playlistID == "" {
		return snapshot{}, webdav.ErrNotFound
	}
	_, tracks, err := l.source.Playlist(ctx, playlistID)
	if err != nil {
		return snapshot{}, err
	}
	s, err := l.makeTrackSnapshot(ctx, directory, tracks)
	if err != nil {
		return snapshot{}, err
	}
	l.store(key, s, l.playlistTTL)
	return s, nil
}

func (l *VirtualLibrary) trackSnapshot(ctx context.Context, directory string) (snapshot, error) {
	key := "tracks:" + directory
	if s, ok := l.snapshot(key); ok {
		return s, nil
	}
	var tracks []model.Track
	var err error
	switch directory {
	case likedRoot:
		tracks, err = l.source.LikedTracks(ctx)
	case dailyRoot:
		tracks, err = l.source.DailyRecommendations(ctx)
	case searchRoot:
		if l.search == nil {
			tracks = []model.Track{}
			break
		}
		identities, listErr := l.search.List(ctx, l.source.ID())
		if listErr != nil {
			return snapshot{}, listErr
		}
		tracks = make([]model.Track, 0, len(identities))
		for _, identity := range identities {
			track, trackErr := l.source.Track(ctx, identity)
			if trackErr == nil {
				tracks = append(tracks, track)
			}
		}
	default:
		return snapshot{}, webdav.ErrNotFound
	}
	if err != nil {
		return snapshot{}, err
	}
	s, err := l.makeTrackSnapshot(ctx, directory, tracks)
	if err != nil {
		return snapshot{}, err
	}
	ttl := l.playlistTTL
	if directory == dailyRoot {
		ttl = l.dailyTTL
	}
	l.store(key, s, ttl)
	return s, nil
}

func (l *VirtualLibrary) makeTrackSnapshot(ctx context.Context, directory string, tracks []model.Track) (snapshot, error) {
	s := emptySnapshot()
	s.resources[directory] = collection(directory)
	// Sort only for collision assignment; this keeps colliding filenames stable
	// even if the upstream ordering changes on a later refresh.
	sorted := append([]model.Track(nil), tracks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Identity.String() < sorted[j].Identity.String() })
	used := make(map[string]bool)
	var firstErr error
	for _, track := range sorted {
		desc, err := l.proxy.Describe(ctx, track.Identity)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		} // unavailable tracks do not manufacture a false representation.
		filename := AudioFilename(track, desc.Format.Extension)
		if used[filename] {
			filename = CollisionFilename(filename, track.Identity)
		}
		used[filename] = true
		audio := l.audioResource(directory+"/"+filename, track.Identity, desc)
		lyrics := webdav.Resource{Path: directory + "/" + LyricsFilename(filename), Kind: webdav.LyricsFile, ContentType: "text/plain; charset=utf-8", Size: -1, ModifiedAt: track.UpdatedAt, Track: track.Identity}
		s.resources[audio.Path] = audio
		s.resources[lyrics.Path] = lyrics
		s.children[directory] = append(s.children[directory], audio, lyrics)
	}
	if len(tracks) > 0 && len(s.children[directory]) == 0 && firstErr != nil {
		return snapshot{}, firstErr
	}
	sortResources(s.children[directory])
	return s, nil
}

func (l *VirtualLibrary) audioResource(path string, id model.TrackIdentity, desc model.ResolvedStream) webdav.Resource {
	copy := desc
	return webdav.Resource{Path: path, Kind: webdav.AudioFile, ContentType: desc.Format.ContentType, Size: desc.Size, ModifiedAt: desc.LastModified, ETag: l.proxy.ETag(id, desc), Track: id, Representation: &copy}
}
func collection(path string) webdav.Resource {
	return webdav.Resource{Path: path, Kind: webdav.Collection, Size: 0}
}
func cleanPath(value string) string {
	if value == "" {
		return "/"
	}
	cleaned := path.Clean(value)
	if cleaned == "/" {
		return "/"
	}
	return strings.TrimSuffix(cleaned, "/")
}
func emptySnapshot() snapshot {
	return snapshot{resources: make(map[string]webdav.Resource), children: make(map[string][]webdav.Resource)}
}
func (l *VirtualLibrary) snapshot(key string) (snapshot, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.snapshots[key]
	return s, ok && time.Now().Before(s.expires)
}
func (l *VirtualLibrary) store(key string, s snapshot, ttl time.Duration) {
	s.expires = time.Now().Add(ttl)
	l.mu.Lock()
	l.snapshots[key] = s
	l.mu.Unlock()
}
func (l *VirtualLibrary) InvalidateSearch() {
	l.mu.Lock()
	delete(l.snapshots, "tracks:"+searchRoot)
	l.mu.Unlock()
}
func cloneResources(items []webdav.Resource) []webdav.Resource {
	return append([]webdav.Resource(nil), items...)
}
func sortResources(items []webdav.Resource) {
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
}
func uniqueDirectoryName(name, id string, used map[string]bool) string {
	base := SanitizeFilename(name)
	if !used[base] {
		used[base] = true
		return base
	}
	candidate := base + " [" + SanitizeFilename(id) + "]"
	used[candidate] = true
	return candidate
}
func selectLyrics(lyrics model.Lyrics, mode string) string {
	switch mode {
	case "original":
		return lyrics.Original
	case "original_romanized":
		return mergeLRC(lyrics.Original, lyrics.Romanized)
	default:
		return mergeLRC(lyrics.Original, lyrics.Translated)
	}
}

// Keep timestamped original LRC intact. Netease translation payloads are also
// timestamped; appending their raw lines is the only non-destructive merge.
func mergeLRC(original, secondary string) string {
	if original == "" {
		return secondary
	}
	if secondary == "" {
		return original
	}
	return strings.TrimRight(original, "\n") + "\n" + strings.TrimLeft(secondary, "\n") + "\n"
}

var _ webdav.Library = (*VirtualLibrary)(nil)
var _ webdav.ContentLibrary = (*VirtualLibrary)(nil)
