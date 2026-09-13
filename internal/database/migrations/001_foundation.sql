CREATE TABLE sources (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    display_name TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE source_sessions (
    source_id TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    encrypted_payload BLOB NOT NULL,
    key_version INTEGER NOT NULL,
    expires_at TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tracks (
    identity TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    upstream_track_id TEXT NOT NULL,
    title TEXT NOT NULL,
    artists_json TEXT NOT NULL,
    album_id TEXT,
    album_title TEXT,
    cover_url TEXT,
    cover_mime_type TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(source_id, upstream_track_id)
);

CREATE TABLE cache_entries (
    cache_key TEXT PRIMARY KEY,
    byte_size INTEGER NOT NULL CHECK(byte_size >= 0),
    content_type TEXT NOT NULL,
    codec TEXT,
    quality TEXT,
    local_path TEXT NOT NULL,
    last_accessed_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX cache_entries_lru ON cache_entries(last_accessed_at);
