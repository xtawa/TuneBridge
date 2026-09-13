package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Entry is an immutable, complete audio object. Partial files are deliberately
// never represented here, so a crash cannot turn an incomplete download into a
// cache hit.
type Entry struct {
	Key         string
	Path        string
	Size        int64
	ContentType string
	Codec       string
	Quality     string
}

type Manager struct {
	db       *sql.DB
	dir      string
	maxBytes int64
	mu       sync.Mutex
	leases   map[string]int
}

func NewAudioManager(db *sql.DB, dir string, maxBytes int64) (*Manager, error) {
	if db == nil || strings.TrimSpace(dir) == "" || maxBytes <= 0 {
		return nil, errors.New("audio cache requires database, directory, and positive limit")
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o750); err != nil {
		return nil, fmt.Errorf("create cache temp directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "objects"), 0o750); err != nil {
		return nil, fmt.Errorf("create cache object directory: %w", err)
	}
	m := &Manager{db: db, dir: dir, maxBytes: maxBytes, leases: make(map[string]int)}
	if err := m.RemovePartials(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) RemovePartials() error {
	entries, err := os.ReadDir(filepath.Join(m.dir, "tmp"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".partial") {
			if err := os.Remove(filepath.Join(m.dir, "tmp", entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) Lookup(ctx context.Context, key string) (Entry, bool, error) {
	var entry Entry
	err := m.db.QueryRowContext(ctx, `SELECT cache_key, local_path, byte_size, content_type, COALESCE(codec, ''), COALESCE(quality, '') FROM cache_entries WHERE cache_key = ?`, key).Scan(&entry.Key, &entry.Path, &entry.Size, &entry.ContentType, &entry.Codec, &entry.Quality)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("read audio cache entry: %w", err)
	}
	info, err := os.Stat(entry.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
		_, _ = m.db.ExecContext(ctx, `DELETE FROM cache_entries WHERE cache_key = ?`, key)
		return Entry{}, false, nil
	}
	if _, err := m.db.ExecContext(ctx, `UPDATE cache_entries SET last_accessed_at = CURRENT_TIMESTAMP WHERE cache_key = ?`, key); err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (m *Manager) NewPartial() (*os.File, string, error) {
	name := uuid.NewString() + ".partial"
	path := filepath.Join(m.dir, "tmp", name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

// Commit closes a fully written temporary file, atomically publishes it and
// records it before LRU pruning. Callers must delete partialPath on any error.
func (m *Manager) Commit(ctx context.Context, partialPath, key, contentType, codec, quality string, size int64) (Entry, error) {
	if size < 0 {
		return Entry{}, errors.New("cache entry size must be known")
	}
	objectPath := filepath.Join(m.dir, "objects", uuid.NewString())
	if err := os.Rename(partialPath, objectPath); err != nil {
		return Entry{}, fmt.Errorf("publish audio cache object: %w", err)
	}
	entry := Entry{Key: key, Path: objectPath, Size: size, ContentType: contentType, Codec: codec, Quality: quality}
	if _, err := m.db.ExecContext(ctx, `INSERT INTO cache_entries(cache_key, byte_size, content_type, codec, quality, local_path) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(cache_key) DO UPDATE SET byte_size=excluded.byte_size, content_type=excluded.content_type, codec=excluded.codec, quality=excluded.quality, local_path=excluded.local_path, last_accessed_at=CURRENT_TIMESTAMP`, key, size, contentType, codec, quality, objectPath); err != nil {
		_ = os.Remove(objectPath)
		return Entry{}, fmt.Errorf("record audio cache object: %w", err)
	}
	if err := m.Evict(ctx); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (m *Manager) Lease(key string) func() {
	m.mu.Lock()
	m.leases[key]++
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if m.leases[key] <= 1 {
			delete(m.leases, key)
		} else {
			m.leases[key]--
		}
		m.mu.Unlock()
	}
}

func (m *Manager) Evict(ctx context.Context) error {
	var used int64
	if err := m.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(byte_size), 0) FROM cache_entries`).Scan(&used); err != nil {
		return err
	}
	for used > m.maxBytes {
		rows, err := m.db.QueryContext(ctx, `SELECT cache_key, local_path, byte_size FROM cache_entries ORDER BY last_accessed_at ASC, created_at ASC`)
		if err != nil {
			return err
		}
		var candidate Entry
		for rows.Next() {
			if err := rows.Scan(&candidate.Key, &candidate.Path, &candidate.Size); err != nil {
				rows.Close()
				return err
			}
			m.mu.Lock()
			leased := m.leases[candidate.Key] > 0
			m.mu.Unlock()
			if !leased {
				break
			}
			candidate = Entry{}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if candidate.Key == "" {
			return nil
		} // all remaining objects are active.
		if _, err := m.db.ExecContext(ctx, `DELETE FROM cache_entries WHERE cache_key = ?`, candidate.Key); err != nil {
			return err
		}
		if err := os.Remove(candidate.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		used -= candidate.Size
	}
	return nil
}

func (m *Manager) MaxBytes() int64 { return m.maxBytes }

// Keep time imported in this package's API contract for future persistence
// migrations without exposing sqlite timestamps to stream callers.
var _ = time.Time{}
