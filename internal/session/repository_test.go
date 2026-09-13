package session

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/xtawa/tunebridge/internal/database"
	"github.com/xtawa/tunebridge/internal/source"
)

func TestRepositoryEncryptsAndRoundTripsSession(t *testing.T) {
	t.Parallel()
	db, err := database.Open(filepath.Join(t.TempDir(), "tunebridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repository, err := NewRepository(db, testKey())
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("MUSIC_U=not-for-logs; __csrf=also-secret")
	if err := repository.Save(context.Background(), "netease", source.Session{Payload: want}); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := db.QueryRow(`SELECT encrypted_payload FROM source_sessions WHERE source_id = 'netease'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == string(want) {
		t.Fatal("session is stored in plaintext")
	}
	got, err := repository.Load(context.Background(), "netease")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != string(want) {
		t.Fatalf("payload = %q", got.Payload)
	}
}

func TestNewCipherRejectsWrongKeySize(t *testing.T) {
	t.Parallel()
	if _, err := NewCipher(base64.RawURLEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected key size error")
	}
}

func testKey() string { return base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }
