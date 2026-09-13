package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
	"time"

	"github.com/xtawa/tunebridge/internal/api"
	"github.com/xtawa/tunebridge/internal/auth"
	"github.com/xtawa/tunebridge/internal/config"
	"github.com/xtawa/tunebridge/internal/webdav"
)

type Server struct {
	config config.Config
	db     *sql.DB
	logger *slog.Logger
	http   *http.Server
}

func NewServer(cfg config.Config, db *sql.DB, logger *slog.Logger, neteaseLogin *api.NeteaseLoginHandler) *Server {
	return NewServerWithLibrary(cfg, db, logger, neteaseLogin, nil, nil, webdav.BootstrapLibrary())
}

func NewServerWithLibrary(cfg config.Config, db *sql.DB, logger *slog.Logger, neteaseLogin *api.NeteaseLoginHandler, searchHandler *api.SearchHandler, artworkHandler *api.ArtworkHandler, library webdav.Library) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	server := &Server{config: cfg, db: db, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.ready)
	if neteaseLogin != nil {
		mux.Handle("POST /api/sources/netease/login/qr", auth.RequireBasic(http.HandlerFunc(neteaseLogin.Begin), cfg.WebDAVUsername, cfg.WebDAVPassword, "TuneBridge"))
		mux.Handle("GET /api/sources/netease/login/qr/{key}", auth.RequireBasic(http.HandlerFunc(neteaseLogin.Check), cfg.WebDAVUsername, cfg.WebDAVPassword, "TuneBridge"))
	}
	if searchHandler != nil {
		mux.Handle("GET /search", auth.RequireBasic(api.NewSearchPageHandler(), cfg.WebDAVUsername, cfg.WebDAVPassword, "TuneBridge"))
		mux.Handle("GET /api/search", auth.RequireBasic(http.HandlerFunc(searchHandler.Search), cfg.WebDAVUsername, cfg.WebDAVPassword, "TuneBridge"))
		mux.Handle("POST /api/search-results", auth.RequireBasic(http.HandlerFunc(searchHandler.Add), cfg.WebDAVUsername, cfg.WebDAVPassword, "TuneBridge"))
		mux.Handle("DELETE /api/search-results", auth.RequireBasic(http.HandlerFunc(searchHandler.Clear), cfg.WebDAVUsername, cfg.WebDAVPassword, "TuneBridge"))
		mux.Handle("DELETE /api/search-results/{trackID}", auth.RequireBasic(http.HandlerFunc(searchHandler.Delete), cfg.WebDAVUsername, cfg.WebDAVPassword, "TuneBridge"))
	}
	if artworkHandler != nil {
		mux.Handle("GET /api/artwork/{sourceID}/{trackID}", auth.RequireBasic(http.HandlerFunc(artworkHandler.Serve), cfg.WebDAVUsername, cfg.WebDAVPassword, "TuneBridge"))
	}
	if library == nil {
		library = webdav.BootstrapLibrary()
	}
	mux.Handle("/", webdav.NewHandler(library, cfg.WebDAVUsername, cfg.WebDAVPassword))
	server.http = &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           requestLog(logger, mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	return server
}

func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.http.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("listen %s: %w", s.config.ListenAddress, err)
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}\n`))
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		http.Error(w, `{"status":"not_ready"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ready"}\n`))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
func (r *statusRecorder) Write(bytes []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(bytes)
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		logger.Info("request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "range", r.Header.Get("Range"), "user_agent", r.UserAgent(), "status", recorder.status, "latency", time.Since(start))
	})
}
