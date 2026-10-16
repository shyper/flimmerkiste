package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shyper/flimmerkiste/internal/config"
	"github.com/shyper/flimmerkiste/internal/logging"
	"github.com/shyper/flimmerkiste/internal/server"
	"github.com/shyper/flimmerkiste/internal/store"
	"github.com/shyper/flimmerkiste/web"
)

// version is set at build time (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	cfg := config.Load()
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	log := slog.New(logging.Redacting(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Error("open database (is the data directory writable for UID 1000?)", "dir", cfg.DataDir, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	server.Version = version
	srv, err := server.New(cfg, st, log, web.Dist())
	if err != nil {
		log.Error("init", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.Background(ctx)

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	log.Info("Flimmerkiste listening", "version", version, "addr", cfg.Listen, "data", cfg.DataDir)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http server", "err", err)
		os.Exit(1)
	}
}
