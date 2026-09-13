package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/xtawa/tunebridge/internal/source"
)

var ErrNotFound = errors.New("source session not found")

type Cipher struct{ aead cipher.AEAD }

func NewCipher(encodedKey string) (*Cipher, error) {
	key, err := decodeKey(encodedKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (c *Cipher) Open(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < c.aead.NonceSize() {
		return nil, errors.New("encrypted session is malformed")
	}
	nonce := ciphertext[:c.aead.NonceSize()]
	return c.aead.Open(nil, nonce, ciphertext[c.aead.NonceSize():], nil)
}

type Repository struct {
	db     *sql.DB
	cipher *Cipher
}

func NewRepository(db *sql.DB, encodedKey string) (*Repository, error) {
	cipher, err := NewCipher(encodedKey)
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, cipher: cipher}, nil
}

func (r *Repository) Save(ctx context.Context, sourceID string, session source.Session) error {
	if sourceID == "" || len(session.Payload) == 0 {
		return errors.New("source ID and session payload are required")
	}
	payload, err := r.cipher.Seal(session.Payload)
	if err != nil {
		return fmt.Errorf("encrypt source session: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin source session write: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sources(id, kind, display_name) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET updated_at = CURRENT_TIMESTAMP`, sourceID, sourceID, sourceID); err != nil {
		return fmt.Errorf("upsert source: %w", err)
	}
	var expiresAt any
	if !session.ExpiresAt.IsZero() {
		expiresAt = session.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO source_sessions(source_id, encrypted_payload, key_version, expires_at) VALUES (?, ?, 1, ?) ON CONFLICT(source_id) DO UPDATE SET encrypted_payload = excluded.encrypted_payload, key_version = excluded.key_version, expires_at = excluded.expires_at, updated_at = CURRENT_TIMESTAMP`, sourceID, payload, expiresAt); err != nil {
		return fmt.Errorf("upsert source session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit source session: %w", err)
	}
	return nil
}

func (r *Repository) Load(ctx context.Context, sourceID string) (source.Session, error) {
	var encrypted []byte
	var rawExpiry sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT encrypted_payload, expires_at FROM source_sessions WHERE source_id = ?`, sourceID).Scan(&encrypted, &rawExpiry)
	if errors.Is(err, sql.ErrNoRows) {
		return source.Session{}, ErrNotFound
	}
	if err != nil {
		return source.Session{}, fmt.Errorf("read source session: %w", err)
	}
	payload, err := r.cipher.Open(encrypted)
	if err != nil {
		return source.Session{}, fmt.Errorf("decrypt source session: %w", err)
	}
	result := source.Session{Payload: payload}
	if rawExpiry.Valid {
		result.ExpiresAt, err = time.Parse(time.RFC3339Nano, rawExpiry.String)
		if err != nil {
			return source.Session{}, fmt.Errorf("parse source session expiry: %w", err)
		}
	}
	return result, nil
}

func decodeKey(encodedKey string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		key, err := encoding.DecodeString(encodedKey)
		if err == nil {
			if len(key) != 32 {
				return nil, errors.New("session encryption key must decode to exactly 32 bytes")
			}
			return key, nil
		}
	}
	return nil, errors.New("session encryption key must be base64 encoded")
}
