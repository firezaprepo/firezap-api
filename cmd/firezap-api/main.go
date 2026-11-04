// Command firezap-api runs the FireZAP API server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/firezaprepo/firezap-api/internal/config"
	"github.com/firezaprepo/firezap-api/internal/httpapi"
	"github.com/firezaprepo/firezap-api/internal/instance"
	"github.com/firezaprepo/firezap-api/internal/store"
	"github.com/firezaprepo/firezap-api/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}

	level := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}[cfg.LogLevel]
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	wlog := waLog.Stdout("whatsmeow", strings.ToUpper(cfg.LogLevel), false)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	st := store.New(db)
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// The name shown on the phone under "Linked devices".
	wastore.SetOSInfo("FireZAP API", [3]uint32{0, 1, 0})
	container := sqlstore.NewWithDB(db, "postgres", wlog.Sub("store"))
	if err := container.Upgrade(ctx); err != nil {
		return fmt.Errorf("prepare session store: %w", err)
	}

	hooks := webhook.NewDispatcher(cfg.WebhookTimeout, log)
	manager := instance.NewManager(st, container, hooks, log, wlog, cfg.SendMinInterval)
	if err := manager.Restore(ctx); err != nil {
		return fmt.Errorf("restore instances: %w", err)
	}
	defer manager.Shutdown()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:           httpapi.New(cfg.AdminKey, manager, st, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("FireZAP API listening", "port", cfg.HTTPPort, "version", httpapi.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	return srv.Shutdown(shutdownCtx)
}
