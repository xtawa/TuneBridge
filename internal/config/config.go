package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultCacheMaxBytes int64 = 10 * 1024 * 1024 * 1024
const defaultArtworkCacheMaxBytes int64 = 1024 * 1024 * 1024

type LookupEnv func(string) (string, bool)

var OSLookupEnv LookupEnv = os.LookupEnv

type Config struct {
	ListenAddress          string
	DataDir                string
	DatabasePath           string
	AudioCacheDir          string
	CacheMaxBytes          int64
	ArtworkCacheDir        string
	ArtworkCacheMaxBytes   int64
	WebDAVUsername         string
	WebDAVPassword         string
	NeteaseAPIBaseURL      string
	SessionEncryptionKey   string
	PlaylistTTL            time.Duration
	DailyRecommendationTTL time.Duration
	PreferredQuality       string
	LyricsMode             string
}

func Load(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("environment lookup is required")
	}

	dataDir := value(lookup, "TUNEBRIDGE_DATA_DIR", "./data")
	cacheMaxBytes, err := bytes(value(lookup, "TUNEBRIDGE_CACHE_MAX_BYTES", strconv.FormatInt(defaultCacheMaxBytes, 10)))
	if err != nil {
		return Config{}, fmt.Errorf("TUNEBRIDGE_CACHE_MAX_BYTES: %w", err)
	}
	artworkMaxBytes, err := bytes(value(lookup, "TUNEBRIDGE_ARTWORK_CACHE_MAX_BYTES", strconv.FormatInt(defaultArtworkCacheMaxBytes, 10)))
	if err != nil {
		return Config{}, fmt.Errorf("TUNEBRIDGE_ARTWORK_CACHE_MAX_BYTES: %w", err)
	}

	cfg := Config{
		ListenAddress:          value(lookup, "TUNEBRIDGE_LISTEN_ADDRESS", ":8080"),
		DataDir:                dataDir,
		DatabasePath:           value(lookup, "TUNEBRIDGE_DATABASE_PATH", filepath.Join(dataDir, "tunebridge.db")),
		AudioCacheDir:          value(lookup, "TUNEBRIDGE_AUDIO_CACHE_DIR", filepath.Join(dataDir, "cache", "audio")),
		CacheMaxBytes:          cacheMaxBytes,
		ArtworkCacheDir:        value(lookup, "TUNEBRIDGE_ARTWORK_CACHE_DIR", filepath.Join(dataDir, "cache", "artwork")),
		ArtworkCacheMaxBytes:   artworkMaxBytes,
		WebDAVUsername:         value(lookup, "TUNEBRIDGE_WEBDAV_USERNAME", ""),
		WebDAVPassword:         value(lookup, "TUNEBRIDGE_WEBDAV_PASSWORD", ""),
		NeteaseAPIBaseURL:      value(lookup, "TUNEBRIDGE_NETEASE_API_BASE_URL", ""),
		SessionEncryptionKey:   value(lookup, "TUNEBRIDGE_SESSION_ENCRYPTION_KEY", ""),
		PlaylistTTL:            duration(lookup, "TUNEBRIDGE_PLAYLIST_TTL", 5*time.Minute),
		DailyRecommendationTTL: duration(lookup, "TUNEBRIDGE_DAILY_RECOMMENDATION_TTL", time.Hour),
		PreferredQuality:       value(lookup, "TUNEBRIDGE_PREFERRED_QUALITY", "lossless"),
		LyricsMode:             value(lookup, "TUNEBRIDGE_LYRICS_MODE", "original_translation"),
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddress) == "" {
		return errors.New("TUNEBRIDGE_LISTEN_ADDRESS must not be empty")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("TUNEBRIDGE_DATA_DIR must not be empty")
	}
	if c.CacheMaxBytes <= 0 || c.ArtworkCacheMaxBytes <= 0 {
		return errors.New("cache limits must be positive")
	}
	if strings.TrimSpace(c.WebDAVUsername) == "" || c.WebDAVPassword == "" {
		return errors.New("TUNEBRIDGE_WEBDAV_USERNAME and TUNEBRIDGE_WEBDAV_PASSWORD are required; anonymous WebDAV is not supported")
	}
	if c.PlaylistTTL <= 0 || c.DailyRecommendationTTL <= 0 {
		return errors.New("metadata TTLs must be positive")
	}
	if c.PreferredQuality != "lossless" && c.PreferredQuality != "exhigh" && c.PreferredQuality != "higher" && c.PreferredQuality != "standard" {
		return errors.New("TUNEBRIDGE_PREFERRED_QUALITY must be lossless, exhigh, higher, or standard")
	}
	if c.LyricsMode != "original" && c.LyricsMode != "original_translation" && c.LyricsMode != "original_romanized" {
		return errors.New("TUNEBRIDGE_LYRICS_MODE must be original, original_translation, or original_romanized")
	}
	if c.NeteaseAPIBaseURL != "" {
		parsed, err := url.ParseRequestURI(c.NeteaseAPIBaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return errors.New("TUNEBRIDGE_NETEASE_API_BASE_URL must be an absolute URL")
		}
		if strings.TrimSpace(c.SessionEncryptionKey) == "" {
			return errors.New("TUNEBRIDGE_SESSION_ENCRYPTION_KEY is required when TUNEBRIDGE_NETEASE_API_BASE_URL is configured")
		}
	}
	return nil
}

func value(lookup LookupEnv, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func bytes(raw string) (int64, error) {
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("must be a positive integer byte count")
	}
	return parsed, nil
}

func duration(lookup LookupEnv, key string, fallback time.Duration) time.Duration {
	raw, ok := lookup(key)
	if !ok || raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
