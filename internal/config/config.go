package config

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	defaultAddress     = ":8080"
	defaultDatabase    = "data/go-content-go.db"
	defaultEnvFilePath = ".env"
	defaultUploadPath  = "uploads"
	defaultUploadLimit = int64(10 << 20)
	defaultThemesPath  = "themes"
	defaultPluginsPath = "plugins"
	defaultBackupPath  = "backups"
	defaultAPIRate     = 60
)

// Config contains the runtime settings for the application.
type Config struct {
	Address       string
	DatabasePath  string
	UploadPath    string
	MaxUploadSize int64
	ThemesPath    string
	PluginsPath   string
	BackupPath    string
	APIRateLimit  int
}

// Load reads settings from flags, environment variables, an optional dotenv
// file, and defaults, in that order of precedence.
func Load(args []string) (Config, error) {
	flags := flag.NewFlagSet("go-content-go", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	addressFlag := flags.String("addr", defaultAddress, "HTTP listen address")
	databaseFlag := flags.String("db", defaultDatabase, "SQLite database path")
	envFileFlag := flags.String("env-file", defaultEnvFilePath, "optional dotenv file path")
	uploadPathFlag := flags.String("uploads", defaultUploadPath, "media upload directory")
	maxUploadSizeFlag := flags.Int64("max-upload-size", defaultUploadLimit, "maximum uploaded file size in bytes")
	themesPathFlag := flags.String("themes", defaultThemesPath, "themes directory")
	pluginsPathFlag := flags.String("plugins", defaultPluginsPath, "plugins directory")
	backupsPathFlag := flags.String("backups", defaultBackupPath, "database backup directory")
	apiRateLimitFlag := flags.Int("api-rate-limit", defaultAPIRate, "maximum REST API requests per IP per minute")
	if err := flags.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}

	envFilePath := *envFileFlag
	if !wasFlagSet(flags, "env-file") {
		if value, ok := os.LookupEnv("GO_CONTENT_GO_ENV_FILE"); ok {
			envFilePath = value
		}
	}

	dotenv, err := readDotenv(envFilePath)
	if err != nil {
		return Config{}, fmt.Errorf("read dotenv file %q: %w", envFilePath, err)
	}

	uploadPath := setting(flags, "uploads", "GO_CONTENT_GO_UPLOADS", *uploadPathFlag, dotenv)
	themesPath := setting(flags, "themes", "GO_CONTENT_GO_THEMES", *themesPathFlag, dotenv)
	pluginsPath := setting(flags, "plugins", "GO_CONTENT_GO_PLUGINS", *pluginsPathFlag, dotenv)
	backupsPath := setting(flags, "backups", "GO_CONTENT_GO_BACKUPS", *backupsPathFlag, dotenv)
	maxUploadSize := *maxUploadSizeFlag
	if !wasFlagSet(flags, "max-upload-size") {
		value, ok := os.LookupEnv("GO_CONTENT_GO_MAX_UPLOAD_SIZE")
		if !ok {
			value, ok = dotenv["GO_CONTENT_GO_MAX_UPLOAD_SIZE"]
		}
		if ok {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return Config{}, fmt.Errorf("parse GO_CONTENT_GO_MAX_UPLOAD_SIZE: %w", err)
			}
			maxUploadSize = parsed
		}
	}
	if maxUploadSize < 1 || maxUploadSize > 100<<20 {
		return Config{}, fmt.Errorf("maximum upload size must be between 1 byte and 100 MiB")
	}
	apiRateLimit := *apiRateLimitFlag
	if !wasFlagSet(flags, "api-rate-limit") {
		value, ok := os.LookupEnv("GO_CONTENT_GO_API_RATE_LIMIT")
		if !ok {
			value, ok = dotenv["GO_CONTENT_GO_API_RATE_LIMIT"]
		}
		if ok {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return Config{}, fmt.Errorf("parse GO_CONTENT_GO_API_RATE_LIMIT: %w", err)
			}
			apiRateLimit = parsed
		}
	}
	if apiRateLimit < 1 || apiRateLimit > 10000 {
		return Config{}, fmt.Errorf("API rate limit must be between 1 and 10000 requests per minute")
	}

	return Config{
		Address:       setting(flags, "addr", "GO_CONTENT_GO_ADDR", *addressFlag, dotenv),
		DatabasePath:  setting(flags, "db", "GO_CONTENT_GO_DB", *databaseFlag, dotenv),
		UploadPath:    uploadPath,
		MaxUploadSize: maxUploadSize,
		ThemesPath:    themesPath,
		PluginsPath:   pluginsPath,
		BackupPath:    backupsPath,
		APIRateLimit:  apiRateLimit,
	}, nil
}

func setting(flags *flag.FlagSet, flagName, envName, flagValue string, dotenv map[string]string) string {
	if wasFlagSet(flags, flagName) {
		return flagValue
	}
	if value, ok := os.LookupEnv(envName); ok {
		return value
	}
	if value, ok := dotenv[envName]; ok {
		return value
	}
	return flagValue
}

func wasFlagSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(parsed *flag.Flag) {
		if parsed.Name == name {
			set = true
		}
	})
	return set
}

func readDotenv(path string) (map[string]string, error) {
	values := make(map[string]string)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid assignment on line %d", lineNumber)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}
