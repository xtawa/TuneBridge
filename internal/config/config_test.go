package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsWithCredentials(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"TUNEBRIDGE_WEBDAV_USERNAME": "player",
		"TUNEBRIDGE_WEBDAV_PASSWORD": "not-logged",
	}
	cfg, err := Load(fromMap(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CacheMaxBytes != defaultCacheMaxBytes || cfg.PlaylistTTL != 5*time.Minute {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadRejectsAnonymousWebDAV(t *testing.T) {
	t.Parallel()
	_, err := Load(fromMap(nil))
	if err == nil || !strings.Contains(err.Error(), "anonymous WebDAV") {
		t.Fatalf("expected anonymous WebDAV rejection, got %v", err)
	}
}

func TestLoadRejectsNonPositiveCache(t *testing.T) {
	t.Parallel()
	_, err := Load(fromMap(map[string]string{
		"TUNEBRIDGE_WEBDAV_USERNAME": "user",
		"TUNEBRIDGE_WEBDAV_PASSWORD": "password",
		"TUNEBRIDGE_CACHE_MAX_BYTES": "0",
	}))
	if err == nil || !strings.Contains(err.Error(), "CACHE_MAX_BYTES") {
		t.Fatalf("expected cache error, got %v", err)
	}
}

func TestLoadRequiresEncryptionKeyForNeteaseAdapter(t *testing.T) {
	t.Parallel()
	_, err := Load(fromMap(map[string]string{
		"TUNEBRIDGE_WEBDAV_USERNAME":      "user",
		"TUNEBRIDGE_WEBDAV_PASSWORD":      "password",
		"TUNEBRIDGE_NETEASE_API_BASE_URL": "http://netease-api:3000",
	}))
	if err == nil || !strings.Contains(err.Error(), "SESSION_ENCRYPTION_KEY") {
		t.Fatalf("expected encryption key error, got %v", err)
	}
}

func TestLoadAcceptsNeteaseAPIURLAlias(t *testing.T) {
	t.Parallel()
	cfg, err := Load(fromMap(map[string]string{
		"TUNEBRIDGE_WEBDAV_USERNAME":       "user",
		"TUNEBRIDGE_WEBDAV_PASSWORD":       "password",
		"TUNEBRIDGE_NETEASE_API_URL":        "http://netease-api:3000",
		"TUNEBRIDGE_SESSION_ENCRYPTION_KEY": "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.NeteaseAPIBaseURL != "http://netease-api:3000" {
		t.Fatalf("expected http://netease-api:3000, got %s", cfg.NeteaseAPIBaseURL)
	}
}

func fromMap(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
