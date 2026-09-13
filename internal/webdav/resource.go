package webdav

import (
	"context"
	"errors"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xtawa/tunebridge/internal/model"
)

var ErrNotFound = errors.New("virtual resource not found")

type ResourceKind uint8

const (
	Collection ResourceKind = iota
	AudioFile
	LyricsFile
	CoverFile
)

type Resource struct {
	Path        string
	Kind        ResourceKind
	ContentType string
	Size        int64 // -1 means the stream length is not known yet.
	ModifiedAt  time.Time
	ETag        string
	Track       model.TrackIdentity
	// Representation is internal routing state, never a serialized DAV field.
	// It freezes codec, size, and byte layout for the lifetime of one request.
	Representation *model.ResolvedStream
}

func (r Resource) IsCollection() bool { return r.Kind == Collection }

// Library is deliberately read-only. The handler does not expose mutation
// methods because OnePlayer's required use case is a virtual music library.
type Library interface {
	Stat(ctx context.Context, resourcePath string) (Resource, error)
	List(ctx context.Context, resourcePath string) ([]Resource, error)
}

// ContentLibrary is implemented by a source-backed virtual library. Keeping it
// separate from Library lets deterministic DAV-only fixtures stay lightweight.
type ContentLibrary interface {
	Head(ctx context.Context, resource Resource) (Resource, error)
	Get(ctx context.Context, w http.ResponseWriter, r *http.Request, resource Resource) error
}

// MemoryLibrary is a deterministic library fixture and initial bootstrap tree.
// The future source-backed library must preserve its identity and path contract.
type MemoryLibrary struct {
	mu        sync.RWMutex
	resources map[string]Resource
}

func NewMemoryLibrary(resources ...Resource) (*MemoryLibrary, error) {
	library := &MemoryLibrary{resources: make(map[string]Resource)}
	for _, resource := range resources {
		if err := library.Put(resource); err != nil {
			return nil, err
		}
	}
	if _, ok := library.resources["/"]; !ok {
		library.resources["/"] = Resource{Path: "/", Kind: Collection}
	}
	return library, nil
}

func BootstrapLibrary() *MemoryLibrary {
	library, err := NewMemoryLibrary(
		Resource{Path: "/", Kind: Collection},
		Resource{Path: "/网易云", Kind: Collection},
		Resource{Path: "/网易云/我喜欢的音乐", Kind: Collection},
		Resource{Path: "/网易云/我的歌单", Kind: Collection},
		Resource{Path: "/网易云/每日推荐", Kind: Collection},
		Resource{Path: "/网易云/搜索结果", Kind: Collection},
	)
	if err != nil {
		panic(err)
	}
	return library
}

func (l *MemoryLibrary) Put(resource Resource) error {
	resourcePath, err := normalizePath(resource.Path)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if resourcePath != "/" {
		parent := path.Dir(resourcePath)
		if _, ok := l.resources[parent]; !ok {
			return errors.New("parent collection does not exist: " + parent)
		}
	}
	if resource.IsCollection() && resource.Size != 0 {
		return errors.New("collection size must be zero")
	}
	resource.Path = resourcePath
	l.resources[resourcePath] = resource
	return nil
}

func (l *MemoryLibrary) Stat(_ context.Context, resourcePath string) (Resource, error) {
	resourcePath, err := normalizePath(resourcePath)
	if err != nil {
		return Resource{}, ErrNotFound
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	resource, ok := l.resources[resourcePath]
	if !ok {
		return Resource{}, ErrNotFound
	}
	return resource, nil
}

func (l *MemoryLibrary) List(_ context.Context, resourcePath string) ([]Resource, error) {
	resourcePath, err := normalizePath(resourcePath)
	if err != nil {
		return nil, ErrNotFound
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	resource, ok := l.resources[resourcePath]
	if !ok || !resource.IsCollection() {
		return nil, ErrNotFound
	}
	entries := make([]Resource, 0)
	for childPath, child := range l.resources {
		if childPath != resourcePath && path.Dir(childPath) == resourcePath {
			entries = append(entries, child)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func normalizePath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("path is empty")
	}
	if strings.ContainsRune(raw, '\x00') || !strings.HasPrefix(raw, "/") || strings.Contains(raw, "../") || strings.HasSuffix(raw, "/..") {
		return "", errors.New("invalid virtual path")
	}
	clean := path.Clean(raw)
	if clean == "." {
		clean = "/"
	}
	if clean != "/" && strings.HasSuffix(clean, "/") {
		clean = strings.TrimSuffix(clean, "/")
	}
	return clean, nil
}
