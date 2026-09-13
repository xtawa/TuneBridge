package netease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
)

var ErrUnauthenticated = errors.New("netease login session is required")

type SessionProvider interface {
	Load(ctx context.Context, sourceID string) (source.Session, error)
}

type SessionStore interface {
	SessionProvider
	Save(ctx context.Context, sourceID string, session source.Session) error
}

type Adapter struct {
	client   *Client
	sessions SessionProvider
}

func NewAdapter(client *Client, sessions SessionProvider) (*Adapter, error) {
	if client == nil || sessions == nil {
		return nil, errors.New("netease client and session provider are required")
	}
	return &Adapter{client: client, sessions: sessions}, nil
}

func (a *Adapter) ID() string { return SourceID }

func (a *Adapter) Authenticate(ctx context.Context, input source.AuthInput) (source.Session, error) {
	if len(input.ImportedSession) != 0 {
		return source.Session{Payload: append([]byte(nil), input.ImportedSession...)}, nil
	}
	status, session, err := a.client.CheckQRCode(ctx, input.QRCodeToken)
	if err != nil {
		return source.Session{}, err
	}
	if status != source.QRLoginAuthorized {
		return source.Session{}, fmt.Errorf("QR login is %s", status)
	}
	return session, nil
}

func (a *Adapter) RefreshSession(ctx context.Context, session source.Session) (source.Session, error) {
	if len(session.Payload) == 0 {
		return source.Session{}, ErrUnauthenticated
	}
	refreshed, err := a.client.RefreshToken(ctx, session)
	if err != nil {
		return source.Session{}, err
	}
	if a.client.native != nil && (!a.client.IsExternal() || a.client.native.baseURL.String() == a.client.baseURL.String()) {
		if _, err := a.client.native.UserProfile(ctx, string(refreshed.Payload)); err != nil {
			return source.Session{}, err
		}
	} else {
		if _, err := a.userProfile(ctx, string(refreshed.Payload)); err != nil {
			return source.Session{}, err
		}
	}
	if store, ok := a.sessions.(SessionStore); ok {
		if err := store.Save(ctx, SourceID, refreshed); err != nil {
			return source.Session{}, fmt.Errorf("persist refreshed session: %w", err)
		}
	}
	return refreshed, nil
}

func (a *Adapter) UserProfile(ctx context.Context) (source.UserProfile, error) {
	cookie, err := a.loadCookie(ctx)
	if err != nil {
		return source.UserProfile{}, err
	}
	if a.client.native != nil && (!a.client.IsExternal() || a.client.native.baseURL.String() == a.client.baseURL.String()) {
		return a.client.native.UserProfile(ctx, cookie)
	}
	return a.userProfile(ctx, cookie)
}

func (a *Adapter) userProfile(ctx context.Context, cookie string) (source.UserProfile, error) {
	var response struct {
		Code int `json:"code"`
		Data struct {
			Profile struct {
				ID       flexibleID `json:"userId"`
				Nickname string     `json:"nickname"`
			} `json:"profile"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if _, err := a.client.get(ctx, "/login/status", nil, cookie, &response); err != nil {
		return source.UserProfile{}, err
	}
	if response.Code != http.StatusOK || response.Data.Profile.ID.String() == "" {
		return source.UserProfile{}, apiError(response.Code, response.Message)
	}
	return source.UserProfile{ID: response.Data.Profile.ID.String(), DisplayName: response.Data.Profile.Nickname}, nil
}

func (a *Adapter) LikedTracks(ctx context.Context) ([]model.Track, error) {
	profile, err := a.UserProfile(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Code    int          `json:"code"`
		IDs     []flexibleID `json:"ids"`
		Message string       `json:"message"`
	}
	if _, err := a.authedGet(ctx, "/likelist", url.Values{"uid": {profile.ID}}, &response); err != nil {
		return nil, err
	}
	if response.Code != http.StatusOK {
		return nil, apiError(response.Code, response.Message)
	}
	ids := make([]string, 0, len(response.IDs))
	for _, id := range response.IDs {
		if id.String() != "" {
			ids = append(ids, id.String())
		}
	}
	return a.tracksByIDs(ctx, ids)
}

func (a *Adapter) Playlists(ctx context.Context) ([]source.Playlist, error) {
	profile, err := a.UserProfile(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Code     int `json:"code"`
		Playlist []struct {
			ID          flexibleID `json:"id"`
			Name        string     `json:"name"`
			Description string     `json:"description"`
		} `json:"playlist"`
		Message string `json:"message"`
	}
	if _, err := a.authedGet(ctx, "/user/playlist", url.Values{"uid": {profile.ID}, "limit": {"1000"}}, &response); err != nil {
		return nil, err
	}
	if response.Code != http.StatusOK {
		return nil, apiError(response.Code, response.Message)
	}
	result := make([]source.Playlist, 0, len(response.Playlist))
	for _, playlist := range response.Playlist {
		if playlist.ID.String() != "" {
			result = append(result, source.Playlist{ID: playlist.ID.String(), Name: playlist.Name, Description: playlist.Description})
		}
	}
	return result, nil
}

func (a *Adapter) Playlist(ctx context.Context, playlistID string) (source.Playlist, []model.Track, error) {
	if strings.TrimSpace(playlistID) == "" {
		return source.Playlist{}, nil, errors.New("playlist ID is required")
	}
	var detail struct {
		Code     int `json:"code"`
		Playlist struct {
			ID          flexibleID `json:"id"`
			Name        string     `json:"name"`
			Description string     `json:"description"`
			TrackCount  int        `json:"trackCount"`
		} `json:"playlist"`
		Message string `json:"message"`
	}
	if _, err := a.authedGet(ctx, "/playlist/detail", url.Values{"id": {playlistID}}, &detail); err != nil {
		return source.Playlist{}, nil, err
	}
	if detail.Code != http.StatusOK || detail.Playlist.ID.String() == "" {
		return source.Playlist{}, nil, apiError(detail.Code, detail.Message)
	}
	playlist := source.Playlist{ID: detail.Playlist.ID.String(), Name: detail.Playlist.Name, Description: detail.Playlist.Description}
	tracks := make([]model.Track, 0, detail.Playlist.TrackCount)
	const pageSize = 500
	for offset := 0; offset < detail.Playlist.TrackCount || (detail.Playlist.TrackCount == 0 && offset == 0); offset += pageSize {
		var page struct {
			Code    int       `json:"code"`
			Songs   []songDTO `json:"songs"`
			Message string    `json:"message"`
		}
		if _, err := a.authedGet(ctx, "/playlist/track/all", url.Values{"id": {playlistID}, "limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}}, &page); err != nil {
			return source.Playlist{}, nil, err
		}
		if page.Code != http.StatusOK {
			return source.Playlist{}, nil, apiError(page.Code, page.Message)
		}
		tracks = append(tracks, tracksFromDTO(page.Songs)...)
		if len(page.Songs) < pageSize {
			break
		}
	}
	return playlist, tracks, nil
}

func (a *Adapter) DailyRecommendations(ctx context.Context) ([]model.Track, error) {
	var response struct {
		Code int `json:"code"`
		Data struct {
			Songs []songDTO `json:"dailySongs"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if _, err := a.authedGet(ctx, "/recommend/songs", nil, &response); err != nil {
		return nil, err
	}
	if response.Code != http.StatusOK {
		return nil, apiError(response.Code, response.Message)
	}
	return tracksFromDTO(response.Data.Songs), nil
}

func (a *Adapter) SearchTracks(ctx context.Context, query string) ([]model.Track, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search query is required")
	}
	var response struct {
		Code   int `json:"code"`
		Result struct {
			Songs []songDTO `json:"songs"`
		} `json:"result"`
		Message string `json:"message"`
	}
	if _, err := a.authedGet(ctx, "/cloudsearch", url.Values{"keywords": {query}, "type": {"1"}, "limit": {"50"}}, &response); err != nil {
		return nil, err
	}
	if response.Code != http.StatusOK {
		return nil, apiError(response.Code, response.Message)
	}
	return tracksFromDTO(response.Result.Songs), nil
}

func (a *Adapter) Track(ctx context.Context, id model.TrackIdentity) (model.Track, error) {
	if err := validateIdentity(id); err != nil {
		return model.Track{}, err
	}
	tracks, err := a.tracksByIDs(ctx, []string{id.TrackID})
	if err != nil {
		return model.Track{}, err
	}
	if len(tracks) != 1 {
		return model.Track{}, errors.New("track was not returned by source")
	}
	return tracks[0], nil
}

func (a *Adapter) Cover(ctx context.Context, id model.TrackIdentity) (model.Cover, error) {
	track, err := a.Track(ctx, id)
	return track.Cover, err
}

func (a *Adapter) Lyrics(ctx context.Context, id model.TrackIdentity) (model.Lyrics, error) {
	if err := validateIdentity(id); err != nil {
		return model.Lyrics{}, err
	}
	var response struct {
		Code int `json:"code"`
		LRC  struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		TLRC struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
		Roman struct {
			Lyric string `json:"lyric"`
		} `json:"romalrc"`
		YRC struct {
			Lyric string `json:"lyric"`
		} `json:"yrc"`
		Message string `json:"message"`
	}
	if _, err := a.authedGet(ctx, "/lyric", url.Values{"id": {id.TrackID}}, &response); err != nil {
		return model.Lyrics{}, err
	}
	if response.Code != http.StatusOK {
		return model.Lyrics{}, apiError(response.Code, response.Message)
	}
	return model.Lyrics{Original: response.LRC.Lyric, Translated: response.TLRC.Lyric, Romanized: response.Roman.Lyric, WordTimedRaw: response.YRC.Lyric}, nil
}

func (a *Adapter) ResolveStream(ctx context.Context, id model.TrackIdentity, quality source.Quality) (model.ResolvedStream, error) {
	if err := validateIdentity(id); err != nil {
		return model.ResolvedStream{}, err
	}
	if !validQuality(quality) {
		return model.ResolvedStream{}, errors.New("unsupported preferred quality")
	}
	var response struct {
		Code int `json:"code"`
		Data []struct {
			URL        string `json:"url"`
			Size       int64  `json:"size"`
			Type       string `json:"type"`
			EncodeType string `json:"encodeType"`
			Bitrate    int    `json:"br"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if _, err := a.authedGet(ctx, "/song/url/v1", url.Values{"id": {id.TrackID}, "level": {string(quality)}}, &response); err != nil {
		return model.ResolvedStream{}, err
	}
	if response.Code != http.StatusOK {
		return model.ResolvedStream{}, apiError(response.Code, response.Message)
	}
	if len(response.Data) != 1 || response.Data[0].URL == "" {
		return model.ResolvedStream{}, errors.New("track is unavailable at the requested quality")
	}
	item := response.Data[0]
	extension := strings.ToLower(item.Type)
	if extension == "" {
		extension = strings.ToLower(item.EncodeType)
	}
	return model.ResolvedStream{URL: item.URL, Size: item.Size, Format: model.AudioFormat{Extension: extension, ContentType: contentType(extension), Codec: item.EncodeType, BitrateKbps: item.Bitrate / 1000}}, nil
}

func (a *Adapter) tracksByIDs(ctx context.Context, ids []string) ([]model.Track, error) {
	if len(ids) == 0 {
		return []model.Track{}, nil
	}
	all := make([]model.Track, 0, len(ids))
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		var response struct {
			Code    int       `json:"code"`
			Songs   []songDTO `json:"songs"`
			Message string    `json:"message"`
		}
		if _, err := a.authedGet(ctx, "/song/detail", url.Values{"ids": {strings.Join(ids[start:end], ",")}}, &response); err != nil {
			return nil, err
		}
		if response.Code != http.StatusOK {
			return nil, apiError(response.Code, response.Message)
		}
		all = append(all, tracksFromDTO(response.Songs)...)
	}
	return all, nil
}

func (a *Adapter) authedGet(ctx context.Context, endpoint string, query url.Values, target any) (http.Header, error) {
	cookie, err := a.loadCookie(ctx)
	if err != nil {
		return nil, err
	}
	return a.client.get(ctx, endpoint, query, cookie, target)
}

func (a *Adapter) loadCookie(ctx context.Context) (string, error) {
	session, err := a.sessions.Load(ctx, SourceID)
	if err != nil || len(session.Payload) == 0 {
		return "", ErrUnauthenticated
	}
	return string(session.Payload), nil
}
func validateIdentity(id model.TrackIdentity) error {
	if err := id.Validate(); err != nil {
		return err
	}
	if id.SourceID != SourceID {
		return errors.New("track identity belongs to a different source")
	}
	return nil
}
func validQuality(quality source.Quality) bool {
	return quality == source.QualityLossless || quality == source.QualityExHigh || quality == source.QualityHigher || quality == source.QualityStandard
}

type flexibleID string

func (id *flexibleID) UnmarshalJSON(value []byte) error {
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil {
		*id = flexibleID(number.String())
		return nil
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return err
	}
	*id = flexibleID(text)
	return nil
}
func (id flexibleID) String() string { return string(id) }

type songDTO struct {
	ID      flexibleID `json:"id"`
	Name    string     `json:"name"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"ar"`
	LegacyArtists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		ID     flexibleID `json:"id"`
		Name   string     `json:"name"`
		PicURL string     `json:"picUrl"`
	} `json:"al"`
	LegacyAlbum struct {
		ID     flexibleID `json:"id"`
		Name   string     `json:"name"`
		PicURL string     `json:"picUrl"`
	} `json:"album"`
	Duration int64 `json:"dt"`
}

func tracksFromDTO(items []songDTO) []model.Track {
	result := make([]model.Track, 0, len(items))
	for _, item := range items {
		if item.ID.String() == "" {
			continue
		}
		artists := item.Artists
		if len(artists) == 0 {
			artists = item.LegacyArtists
		}
		album := item.Album
		if album.ID.String() == "" && album.Name == "" {
			album = item.LegacyAlbum
		}
		names := make([]string, 0, len(artists))
		for _, artist := range artists {
			if artist.Name != "" {
				names = append(names, artist.Name)
			}
		}
		result = append(result, model.Track{Identity: model.TrackIdentity{SourceID: SourceID, TrackID: item.ID.String()}, Title: item.Name, Artists: names, AlbumID: album.ID.String(), AlbumTitle: album.Name, Duration: time.Duration(item.Duration) * time.Millisecond, Cover: model.Cover{URL: album.PicURL}})
	}
	return result
}
func contentType(extension string) string {
	switch extension {
	case "mp3":
		return "audio/mpeg"
	case "flac":
		return "audio/flac"
	case "m4a", "aac":
		return "audio/mp4"
	case "ogg", "opus":
		return "audio/ogg"
	case "wav":
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

var _ source.MusicSource = (*Adapter)(nil)
