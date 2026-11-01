package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Luw17/go-content-go/internal/config"
	"github.com/Luw17/go-content-go/internal/database"
	"github.com/Luw17/go-content-go/internal/web"
)

func main() {
	if err := run(); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	ctx := context.Background()
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	if err := database.Migrate(ctx, db); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}

	handler, err := web.NewHandler(db, web.Options{
		UploadDir:     cfg.UploadPath,
		MaxUploadSize: cfg.MaxUploadSize,
		ThemeDir:      cfg.ThemesPath,
		PluginDir:     cfg.PluginsPath,
		BackupDir:     cfg.BackupPath,
	})
	if err != nil {
		return fmt.Errorf("initialize web handler: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	log.Printf("Go Content Go! listening on %s", cfg.Address)
	shutdownContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	scheduledBackupsDone := make(chan struct{})
	go func() {
		defer close(scheduledBackupsDone)
		database.RunScheduledBackups(shutdownContext, db, cfg.BackupPath, 24*time.Hour)
	}()

	select {
	case err := <-serverErrors:
		stop()
		<-scheduledBackupsDone
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-shutdownContext.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		stop()
		<-scheduledBackupsDone
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	stop()
	<-scheduledBackupsDone
	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}
