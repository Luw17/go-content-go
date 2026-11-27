package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/config"
	"github.com/Luw17/go-content-go/internal/content"
	"github.com/Luw17/go-content-go/internal/database"
	"github.com/Luw17/go-content-go/internal/web"
)

var version = "2.1.0-dev"

func main() {
	if err := runCLI(os.Args[1:], os.Stdout); err != nil {
		log.Printf("gcg: %v", err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return runServe(nil)
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "version", "-version", "--version":
		_, err := fmt.Fprintf(stdout, "gcg %s\n", version)
		return err
	case "backup":
		return runBackup(args[1:], stdout)
	case "reset-password":
		return runResetPassword(args[1:], stdout)
	case "disable-2fa":
		return runDisable2FA(args[1:], stdout)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		if strings.HasPrefix(args[0], "-") {
			return runServe(args)
		}
		return fmt.Errorf("unknown command %q (run 'gcg help' for usage)", args[0])
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `gcg — Go Content Go! single-binary CMS CLI (%s)

Usage:
  gcg [flags]                                Start the HTTP server (alias for 'gcg serve')
  gcg serve [flags]                          Start the HTTP server
  gcg version                                Print the binary version
  gcg backup [flags]                         Run an immediate SQLite hot backup and apply retention
  gcg reset-password --user <name> [flags]   Reset a user's password and revoke active sessions
  gcg disable-2fa --user <name> [flags]      Disable 2FA (TOTP and recovery codes) for a user
  gcg help                                   Show this help message

Environment variables:
  GCG_ADDR / GCG_PORT, GCG_DB, GCG_UPLOADS, GCG_BACKUPS, GCG_BACKUP_RETENTION,
  GCG_THEMES, GCG_PLUGINS, GCG_API_RATE_LIMIT, GCG_TRUSTED_PROXIES
  (all GO_CONTENT_GO_* variables remain supported for backwards compatibility)
`, version)
}

func runServe(args []string) error {
	cfg, err := config.Load(args)
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
		UploadDir:       cfg.UploadPath,
		MaxUploadSize:   cfg.MaxUploadSize,
		ThemeDir:        cfg.ThemesPath,
		PluginDir:       cfg.PluginsPath,
		BackupDir:       cfg.BackupPath,
		BackupRetention: cfg.BackupRetention,
		APIRateLimit:    cfg.APIRateLimit,
		TrustedProxies:  cfg.TrustedProxies,
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

	log.Printf("Go Content Go! (gcg %s) listening on %s", version, cfg.Address)
	shutdownContext, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	scheduledBackupsDone := make(chan struct{})
	go func() {
		defer close(scheduledBackupsDone)
		database.RunScheduledBackupsWithRetention(shutdownContext, db, cfg.BackupPath, 24*time.Hour, cfg.BackupRetention)
	}()
	scheduledPublishDone := make(chan struct{})
	go func() {
		defer close(scheduledPublishDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-shutdownContext.Done():
				return
			case <-ticker.C:
				if published, err := content.PublishDueScheduled(shutdownContext, db, time.Now()); err != nil && shutdownContext.Err() == nil {
					slog.Error("publish scheduled content", "error", err)
				} else if published > 0 {
					if clearer, ok := handler.(interface{ ClearCache() }); ok {
						clearer.ClearCache()
					}
					slog.Info("published scheduled content", "count", published)
				}
			}
		}
	}()

	select {
	case err := <-serverErrors:
		stop()
		<-scheduledBackupsDone
		<-scheduledPublishDone
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
		<-scheduledPublishDone
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	stop()
	<-scheduledBackupsDone
	<-scheduledPublishDone
	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

func runBackup(args []string, stdout io.Writer) error {
	cfg, err := config.Load(args)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	path, err := database.HotBackup(ctx, db, cfg.BackupPath)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	removed, err := database.PruneBackups(cfg.BackupPath, cfg.BackupRetention)
	if err != nil {
		return fmt.Errorf("prune backups: %w", err)
	}
	_, err = fmt.Fprintf(stdout, "Backup created: %s (pruned: %d, retention: %d)\n", path, removed, cfg.BackupRetention)
	return err
}

func runResetPassword(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("gcg reset-password", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("user", "", "username to reset password for")
	password := fs.String("password", "", "new password (12-72 bytes; auto-generated if omitted)")
	dbFlag := fs.String("db", "", "SQLite database path")
	envFileFlag := fs.String("env-file", "", "optional dotenv file path")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if strings.TrimSpace(*username) == "" {
		return fmt.Errorf("--user is required")
	}
	cfg, err := loadAdminCLIConfig(*dbFlag, *envFileFlag)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	user, err := auth.FindUserByUsername(ctx, db, *username)
	if err != nil {
		return fmt.Errorf("find user %q: %w", *username, err)
	}

	newPassword := *password
	generated := false
	if newPassword == "" {
		raw := make([]byte, 15)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("generate temporary password: %w", err)
		}
		newPassword = base64.RawURLEncoding.EncodeToString(raw)
		generated = true
	}
	if err := auth.UpdateUserPassword(ctx, db, user.ID, newPassword); err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	if generated {
		_, err = fmt.Fprintf(stdout, "Password reset for %q (all sessions revoked).\nNew password: %s\n", user.Username, newPassword)
		return err
	}
	_, err = fmt.Fprintf(stdout, "Password reset for %q (all sessions revoked).\n", user.Username)
	return err
}

func runDisable2FA(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("gcg disable-2fa", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("user", "", "username to disable 2FA for")
	dbFlag := fs.String("db", "", "SQLite database path")
	envFileFlag := fs.String("env-file", "", "optional dotenv file path")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if strings.TrimSpace(*username) == "" {
		return fmt.Errorf("--user is required")
	}
	cfg, err := loadAdminCLIConfig(*dbFlag, *envFileFlag)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	user, err := auth.FindUserByUsername(ctx, db, *username)
	if err != nil {
		return fmt.Errorf("find user %q: %w", *username, err)
	}
	if err := auth.DisableTOTP(ctx, db, user.ID); err != nil {
		return fmt.Errorf("disable 2FA: %w", err)
	}
	_, err = fmt.Fprintf(stdout, "2FA disabled for %q (TOTP secret and recovery codes cleared).\n", user.Username)
	return err
}

func loadAdminCLIConfig(dbFlag, envFileFlag string) (config.Config, error) {
	var cfgArgs []string
	if envFileFlag != "" {
		cfgArgs = append(cfgArgs, "-env-file", envFileFlag)
	}
	if dbFlag != "" {
		cfgArgs = append(cfgArgs, "-db", dbFlag)
	}
	cfg, err := config.Load(cfgArgs)
	if err != nil {
		return config.Config{}, fmt.Errorf("load configuration: %w", err)
	}
	return cfg, nil
}
