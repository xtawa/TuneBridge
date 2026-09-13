package source

import (
	"context"
	"time"

	"github.com/xtawa/tunebridge/internal/model"
)

// MusicSource isolates provider-specific API behavior from the virtual library
// and HTTP/WebDAV layers. Credentials stay in the adapter/session store and are
// deliberately absent from all return values.
type MusicSource interface {
	ID() string
	Authenticate(ctx context.Context, input AuthInput) (Session, error)
	RefreshSession(ctx context.Context, session Session) (Session, error)
	UserProfile(ctx context.Context) (UserProfile, error)
	LikedTracks(ctx context.Context) ([]model.Track, error)
	LikeTrack(ctx context.Context, trackID string, like bool) error
	Playlists(ctx context.Context) ([]Playlist, error)
	Playlist(ctx context.Context, playlistID string) (Playlist, []model.Track, error)
	DailyRecommendations(ctx context.Context) ([]model.Track, error)
	SearchTracks(ctx context.Context, query string) ([]model.Track, error)
	Track(ctx context.Context, id model.TrackIdentity) (model.Track, error)
	Cover(ctx context.Context, id model.TrackIdentity) (model.Cover, error)
	Lyrics(ctx context.Context, id model.TrackIdentity) (model.Lyrics, error)
	ResolveStream(ctx context.Context, id model.TrackIdentity, quality Quality) (model.ResolvedStream, error)
}

type AuthInput struct {
	QRCodeToken     string
	ImportedSession []byte
}

// Session payloads are passed only to the encrypted session repository; callers
// must not log or serialize them into HTTP responses.
type Session struct {
	Payload   []byte
	ExpiresAt time.Time
}

type QRLoginSource interface {
	CreateQRCode(ctx context.Context) (QRCode, error)
	CheckQRCode(ctx context.Context, key string) (QRLoginStatus, Session, error)
}

type QRCode struct {
	Key       string
	URL       string
	ImageData string
}

type QRLoginStatus string

const (
	QRLoginWaiting              QRLoginStatus = "waiting"
	QRLoginAwaitingConfirmation QRLoginStatus = "awaiting_confirmation"
	QRLoginAuthorized           QRLoginStatus = "authorized"
	QRLoginExpired              QRLoginStatus = "expired"
)

type UserProfile struct {
	ID          string
	DisplayName string
}

type Playlist struct {
	ID          string
	Name        string
	Description string
}

type Quality string

const (
	QualityLossless Quality = "lossless"
	QualityExHigh   Quality = "exhigh"
	QualityHigher   Quality = "higher"
	QualityStandard Quality = "standard"
)
