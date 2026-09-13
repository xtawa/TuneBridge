package model

import (
	"fmt"
	"strings"
	"time"
)

// TrackIdentity is stable across virtual-directory refreshes. Display paths must
// never be used as an identifier because names are user-facing and can collide.
type TrackIdentity struct {
	SourceID string
	TrackID  string
}

func (i TrackIdentity) String() string { return i.SourceID + ":" + i.TrackID }

func (i TrackIdentity) Validate() error {
	if strings.TrimSpace(i.SourceID) == "" || strings.TrimSpace(i.TrackID) == "" {
		return fmt.Errorf("track identity requires source and track IDs")
	}
	return nil
}

type Cover struct {
	URL      string
	MIMEType string
}

type Lyrics struct {
	Original     string
	Translated   string
	Romanized    string
	WordTimedRaw string
}

func (l Lyrics) IsEmpty() bool {
	return l.Original == "" && l.Translated == "" && l.Romanized == "" && l.WordTimedRaw == ""
}

type Track struct {
	Identity    TrackIdentity
	Title       string
	Artists     []string
	AlbumID     string
	AlbumTitle  string
	AlbumArtist string
	TrackNumber int
	DiscNumber  int
	ReleaseDate string
	Year        int
	Genre       string
	ISRC        string
	Duration    time.Duration
	Cover       Cover
	Lyrics      Lyrics
	UpdatedAt   time.Time
}

// AudioFormat is discovered from the resolved upstream stream. It is not a
// preference: filename extension and Content-Type must agree with this value.
type AudioFormat struct {
	Extension   string
	ContentType string
	Codec       string
	BitrateKbps int
}

type ResolvedStream struct {
	URL          string
	ExpiresAt    time.Time
	Size         int64 // -1 means unknown.
	Format       AudioFormat
	ETag         string
	LastModified time.Time
}
