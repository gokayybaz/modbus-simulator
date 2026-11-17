// Command dashboard serves the React dashboard and a JSON API that reads the
// collector's TimescaleDB tables.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"modbus-dashboard/internal/api"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("dashboard stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := pgxpool.ParseConfig(env("DATABASE_URL", "postgres://collector:collector@localhost:5432/machine?sslmode=disable"))
	if err != nil {
		return err
	}
	cfg.MaxConns = 8
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	var pool *pgxpool.Pool
	for wait := time.Second; ; wait = min(wait*2, 10*time.Second) {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				break
			}
			pool.Close()
		}
		slog.Warn("database not ready", "err", err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
	defer pool.Close()

	poll, err := time.ParseDuration(env("POLL_INTERVAL", "1s"))
	if err != nil || poll < 200*time.Millisecond {
		poll = time.Second
	}
	mux := http.NewServeMux()
	(&api.Server{DB: pool, OnlineAfter: max(5*time.Second, 5*poll), PollHint: poll}).Routes(mux)
	mux.Handle("/", spa(env("STATIC_DIR", "web/dist")))

	addr := env("LISTEN_ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: logRequests(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("dashboard listening", "addr", addr, "poll", poll)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// spa serves static files and falls back to index.html for client routes.
func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			if !strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeFile(w, r, filepath.Join(dir, "index.html"))
				return
			}
		} else if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fs.ServeHTTP(w, r)
	})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		if d := time.Since(start); d > 500*time.Millisecond {
			slog.Warn("slow request", "path", r.URL.Path, "query", r.URL.RawQuery, "dur", d)
		}
	})
}
