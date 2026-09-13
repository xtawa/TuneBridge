package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/xtawa/tunebridge/internal/api"
	"github.com/xtawa/tunebridge/internal/app"
	"github.com/xtawa/tunebridge/internal/artwork"
	"github.com/xtawa/tunebridge/internal/cache"
	"github.com/xtawa/tunebridge/internal/compattrace"
	"github.com/xtawa/tunebridge/internal/config"
	"github.com/xtawa/tunebridge/internal/database"
	"github.com/xtawa/tunebridge/internal/library"
	"github.com/xtawa/tunebridge/internal/search"
	"github.com/xtawa/tunebridge/internal/session"
	"github.com/xtawa/tunebridge/internal/source"
	"github.com/xtawa/tunebridge/internal/source/netease"
	"github.com/xtawa/tunebridge/internal/stream"
	"github.com/xtawa/tunebridge/internal/webdav"
)

func main() {
	cfg, err := config.Load(config.OSLookupEnv)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Clean(cfg.AudioCacheDir), 0o750); err != nil {
		slog.Error("create audio cache directory", "error", err)
		os.Exit(1)
	}

	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	var loginHandler *api.NeteaseLoginHandler
	var searchHandler *api.SearchHandler
	var artworkHandler *api.ArtworkHandler
	var davLibrary webdav.Library = webdav.BootstrapLibrary()
	encKey, err := ensureSessionEncryptionKey(cfg.DataDir, cfg.SessionEncryptionKey)
	if err != nil {
		slog.Error("configure encrypted session key", "error", err)
		os.Exit(1)
	}
	repository, err := session.NewRepository(db, encKey)
	if err != nil {
		slog.Error("configure encrypted session storage", "error", err)
		os.Exit(1)
	}
	client, err := netease.New(cfg.NeteaseAPIBaseURL, nil)
	if err != nil {
		slog.Error("configure netease client", "error", err)
		os.Exit(1)
	}
	loginHandler = api.NewNeteaseLoginHandler(client, repository)
	adapter, err := netease.NewAdapter(client, repository)
	if err != nil {
		slog.Error("configure netease adapter", "error", err)
		os.Exit(1)
	}
	audioCache, err := cache.NewAudioManager(db, cfg.AudioCacheDir, cfg.CacheMaxBytes)
	if err != nil {
		slog.Error("configure audio cache", "error", err)
		os.Exit(1)
	}
	proxy, err := stream.NewProxy(adapter, source.Quality(cfg.PreferredQuality), nil, audioCache)
	if err != nil {
		slog.Error("configure stream proxy", "error", err)
		os.Exit(1)
	}
	searchStore, err := search.NewStore(db)
	if err != nil {
		slog.Error("configure search results", "error", err)
		os.Exit(1)
	}
	davLibrary, err = library.NewVirtualLibrary(adapter, proxy, searchStore, cfg.PlaylistTTL, cfg.DailyRecommendationTTL, cfg.LyricsMode)
	if err != nil {
		slog.Error("configure virtual library", "error", err)
		os.Exit(1)
	}
	searchHandler = api.NewSearchHandler(adapter, searchStore, davLibrary.(*library.VirtualLibrary))
	artworkCache, err := artwork.New(adapter, nil, cfg.ArtworkCacheDir, cfg.ArtworkCacheMaxBytes)
	if err != nil {
		slog.Error("configure artwork cache", "error", err)
		os.Exit(1)
	}
	artworkHandler = api.NewArtworkHandler(adapter.ID(), artworkCache)

	var trace *compattrace.Ring
	if cfg.CompatibilityTrace {
		trace = compattrace.New(200)
	}
	server := app.NewServerWithTrace(cfg, db, slog.Default(), loginHandler, searchHandler, artworkHandler, trace, davLibrary)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func ensureSessionEncryptionKey(dataDir, configuredKey string) (string, error) {
	if strings.TrimSpace(configuredKey) != "" {
		return configuredKey, nil
	}
	keyFile := filepath.Join(dataDir, "session.key")
	if data, err := os.ReadFile(keyFile); err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			return key, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random session key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	if err := os.WriteFile(keyFile, []byte(encoded+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("persist session key to %s: %w", keyFile, err)
	}
	return encoded, nil
}
