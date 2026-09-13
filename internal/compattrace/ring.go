package compattrace

import (
	"sync"
	"time"
)

// Entry intentionally excludes request query, authorization, cookies, and all
// provider URLs. It exists solely to identify WebDAV client behavior.
type Entry struct {
	Timestamp       time.Time `json:"timestamp"`
	RequestID       string    `json:"request_id"`
	Method          string    `json:"method"`
	Path            string    `json:"path"`
	UserAgent       string    `json:"user_agent"`
	Depth           string    `json:"depth,omitempty"`
	Range           string    `json:"range,omitempty"`
	IfNoneMatch     string    `json:"if_none_match,omitempty"`
	IfModifiedSince string    `json:"if_modified_since,omitempty"`
	Status          int       `json:"status"`
	ContentType     string    `json:"content_type,omitempty"`
	ContentLength   string    `json:"content_length,omitempty"`
	DurationMillis  int64     `json:"duration_ms"`
}

type Ring struct {
	mu      sync.Mutex
	entries []Entry
	next    int
	full    bool
}

func New(size int) *Ring {
	if size < 1 {
		size = 100
	}
	return &Ring{entries: make([]Entry, size)}
}
func (r *Ring) Add(entry Entry) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.entries[r.next] = entry
	r.next = (r.next + 1) % len(r.entries)
	if r.next == 0 {
		r.full = true
	}
	r.mu.Unlock()
}
func (r *Ring) Recent() []Entry {
	if r == nil {
		return []Entry{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	count := r.next
	if r.full {
		count = len(r.entries)
	}
	result := make([]Entry, 0, count)
	if r.full {
		result = append(result, r.entries[r.next:]...)
	}
	result = append(result, r.entries[:r.next]...)
	return result
}
