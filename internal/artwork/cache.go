package artwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xtawa/tunebridge/internal/model"
	"github.com/xtawa/tunebridge/internal/source"
)

const maxArtworkBytes = 12 << 20

type Service struct {
	source   source.MusicSource
	client   *http.Client
	dir      string
	maxBytes int64
}

func New(src source.MusicSource, client *http.Client, dir string, maxBytes int64) (*Service, error) {
	if src == nil || dir == "" || maxBytes <= 0 {
		return nil, errors.New("artwork cache requires source, directory, and limit")
	}
	if client == nil {
		client = &http.Client{}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Service{source: src, client: client, dir: dir, maxBytes: maxBytes}, nil
}
func (s *Service) Serve(ctx context.Context, w http.ResponseWriter, id model.TrackIdentity) error {
	cover, err := s.source.Cover(ctx, id)
	if err != nil {
		return err
	}
	if cover.URL == "" {
		return os.ErrNotExist
	}
	parsed, err := url.Parse(cover.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return errors.New("source returned unsafe artwork URL")
	}
	hash := sha256.Sum256([]byte(cover.URL))
	name := hex.EncodeToString(hash[:])
	path := filepath.Join(s.dir, name+".image")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.fetch(ctx, cover.URL, path); err != nil {
			return err
		}
		info, err = os.Stat(path)
	}
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	w.Header().Set("Content-Type", contentType(cover.MIMEType, path))
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.Header().Set("ETag", `"`+name+`"`)
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, err = io.Copy(w, file)
	return err
}
func (s *Service) fetch(ctx context.Context, rawURL, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("artwork upstream status %d", response.StatusCode)
	}
	if response.ContentLength > maxArtworkBytes {
		return errors.New("artwork is too large")
	}
	temp, err := os.CreateTemp(s.dir, "*.partial")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	written, copyErr := io.Copy(temp, io.LimitReader(response.Body, maxArtworkBytes+1))
	if closeErr := temp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	if written > maxArtworkBytes {
		return errors.New("artwork is too large")
	}
	if err := os.Rename(tempName, destination); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return s.evict(destination)
}
func (s *Service) evict(protected string) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	type item struct {
		path string
		size int64
		mod  int64
	}
	items := []item{}
	var total int64
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".image") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		items = append(items, item{filepath.Join(s.dir, entry.Name()), info.Size(), info.ModTime().UnixNano()})
		total += info.Size()
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod < items[j].mod })
	for _, item := range items {
		if total <= s.maxBytes {
			break
		}
		if item.path == protected {
			continue
		}
		if err := os.Remove(item.path); err == nil {
			total -= item.size
		}
	}
	return nil
}
func contentType(value, _ string) string {
	switch strings.ToLower(value) {
	case "image/png":
		return "image/png"
	case "image/webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}
