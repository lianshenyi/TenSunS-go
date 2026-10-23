// Command tensuns is the Go rewrite of the TenSunS backend.
//
// Phase 1 currently provides the compatibility foundation: startup checks,
// skey bootstrap, AES/token formats, Consul store, dynamic scheduler loading,
// and the read APIs used by the migration baseline's smoke test. Cloud/JMS
// execution adapters are intentionally injected later via internal/jobs.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/auth"
	"github.com/lianshenyi/TenSunS-go/internal/config"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/httpapi"
	"github.com/lianshenyi/TenSunS-go/internal/jobs"
	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
	"github.com/lianshenyi/TenSunS-go/internal/version"
)

func main() {
	cfg := config.Load()
	logger := newLogger(cfg.LogLevel)
	logger.Info("starting TenSunS Go backend",
		"version", version.Version,
		"commit", version.Commit,
		"built", version.Date,
		"listen", cfg.ListenAddr,
		"consul", cfg.ConsulURL,
		"timezone", time.Local.String(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	consulHTTP := &http.Client{Timeout: 15 * time.Second}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := consul.CheckAPI(probeCtx, cfg.ConsulURL, cfg.ConsulToken, consulHTTP)
	cancel()
	if err != nil {
		logger.Error("consul startup check failed", "error", err)
		os.Exit(1)
	}
	logger.Info("consul authentication and connectivity verified")

	st := consul.New(cfg.ConsulURL, cfg.ConsulToken, consulHTTP)
	skey, err := consul.EnsureSKey(ctx, st, logger)
	if err != nil {
		logger.Error("skey initialization failed", "error", err)
		os.Exit(1)
	}
	cipher, err := cryptox.NewCipher(skey)
	if err != nil {
		logger.Error("invalid skey", "error", err)
		os.Exit(1)
	}
	tokenSigner := cryptox.NewTokenSigner(skey)
	authSvc := auth.New(cfg.AdminPasswd, tokenSigner)

	runtime := &jobs.Runtime{}
	sched := scheduler.New(runtime, logger)
	defer sched.Close()

	if cfg.SchedulerDisabled {
		logger.Warn("scheduler is disabled; existing tasks will not run")
	} else {
		loaded, skipped, err := jobs.LoadFromConsul(ctx, st, sched, logger)
		if err != nil {
			logger.Error("load tasks from Consul failed", "error", err)
			os.Exit(1)
		}
		logger.Info("tasks loaded from Consul", "loaded", loaded, "skipped", skipped)
		if loaded > 0 {
			logger.Warn("cloud/JMS execution adapters are not wired in this skeleton; scheduled jobs will log errors until injected")
		}
	}

	api := httpapi.New(st, cipher, authSvc, sched, logger)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Minute,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP API listening", "addr", cfg.ListenAddr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown failed", "error", err)
	}
	logger.Info("TenSunS Go backend stopped")
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch strings.ToUpper(level) {
	case "DEBUG":
		l = slog.LevelDebug
	case "WARN", "WARNING":
		l = slog.LevelWarn
	case "ERROR":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
