CREATE TABLE search_results (
    source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    upstream_track_id TEXT NOT NULL,
    position INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(source_id, upstream_track_id)
);

CREATE INDEX search_results_position ON search_results(position, created_at);
