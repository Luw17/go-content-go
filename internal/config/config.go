package config

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

const (
	defaultAddress         = ":8080"
	defaultDatabase        = "data/go-content-go.db"
	defaultEnvFilePath     = ".env"
	defaultUploadPath      = "uploads"
	defaultUploadLimit     = int64(10 << 20)
	defaultThemesPath      = "themes"
	defaultPluginsPath     = "plugins"
	defaultBackupPath      = "backups"
	defaultBackupRetention = 10
	defaultAPIRate         = 60
)

// Config contains the runtime settings for the application.
type Config struct {
	Address         string
	DatabasePath    string
	UploadPath      string
	MaxUploadSize   int64
	ThemesPath      string
	PluginsPath     string
	BackupPath      string
	BackupRetention int
	APIRateLimit    int
	TrustedProxies  []string
}

// Load reads settings from flags, environment variables (GCG_* with backwards-compatible
// GO_CONTENT_GO_* fallbacks), an optional dotenv file, and defaults, in that order of precedence.
func Load(args []string) (Config, error) {
	flags := flag.NewFlagSet("gcg", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	addressFlag := flags.String("addr", defaultAddress, "HTTP listen address")
	databaseFlag := flags.String("db", defaultDatabase, "SQLite database path")
	envFileFlag := flags.String("env-file", defaultEnvFilePath, "optional dotenv file path")
	uploadPathFlag := flags.String("uploads", defaultUploadPath, "media upload directory")
	maxUploadSizeFlag := flags.Int64("max-upload-size", defaultUploadLimit, "maximum uploaded file size in bytes")
	themesPathFlag := flags.String("themes", defaultThemesPath, "themes directory")
	pluginsPathFlag := flags.String("plugins", defaultPluginsPath, "plugins directory")
	backupsPathFlag := flags.String("backups", defaultBackupPath, "database backup directory")
	backupRetentionFlag := flags.Int("backup-retention", defaultBackupRetention, "maximum number of retained local backups")
	apiRateLimitFlag := flags.Int("api-rate-limit", defaultAPIRate, "maximum REST API requests per IP per minute")
	trustedProxiesFlag := flags.String("trusted-proxies", "", "comma-separated list of trusted reverse proxy IPs or CIDRs")
	if err := flags.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}

	envFilePath := *envFileFlag
	if !wasFlagSet(flags, "env-file") {
		if value, ok := lookupEnvAny("GCG_ENV_FILE", "GO_CONTENT_GO_ENV_FILE"); ok {
			envFilePath = value
		}
	}

	dotenv, err := readDotenv(envFilePath)
	if err != nil {
		return Config{}, fmt.Errorf("read dotenv file %q: %w", envFilePath, err)
	}

	address := resolveAddress(flags, *addressFlag, dotenv)
	databasePath := setting(flags, "db", *databaseFlag, dotenv, "GCG_DB", "GO_CONTENT_GO_DB")
	uploadPath := setting(flags, "uploads", *uploadPathFlag, dotenv, "GCG_UPLOADS", "GO_CONTENT_GO_UPLOADS")
	themesPath := setting(flags, "themes", *themesPathFlag, dotenv, "GCG_THEMES", "GO_CONTENT_GO_THEMES")
	pluginsPath := setting(flags, "plugins", *pluginsPathFlag, dotenv, "GCG_PLUGINS", "GO_CONTENT_GO_PLUGINS")
	backupsPath := setting(flags, "backups", *backupsPathFlag, dotenv, "GCG_BACKUPS", "GO_CONTENT_GO_BACKUPS")

	maxUploadSize := *maxUploadSizeFlag
	if !wasFlagSet(flags, "max-upload-size") {
		if value, key, ok := lookupSetting(dotenv, "GCG_MAX_UPLOAD_SIZE", "GO_CONTENT_GO_MAX_UPLOAD_SIZE"); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return Config{}, fmt.Errorf("parse %s: %w", key, err)
			}
			maxUploadSize = parsed
		}
	}
	if maxUploadSize < 1 || maxUploadSize > 100<<20 {
		return Config{}, fmt.Errorf("maximum upload size must be between 1 byte and 100 MiB")
	}

	apiRateLimit := *apiRateLimitFlag
	if !wasFlagSet(flags, "api-rate-limit") {
		if value, key, ok := lookupSetting(dotenv, "GCG_API_RATE_LIMIT", "GO_CONTENT_GO_API_RATE_LIMIT"); ok {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return Config{}, fmt.Errorf("parse %s: %w", key, err)
			}
			apiRateLimit = parsed
		}
	}
	if apiRateLimit < 1 || apiRateLimit > 10000 {
		return Config{}, fmt.Errorf("API rate limit must be between 1 and 10000 requests per minute")
	}

	backupRetention := *backupRetentionFlag
	if !wasFlagSet(flags, "backup-retention") {
		if value, key, ok := lookupSetting(dotenv, "GCG_BACKUP_RETENTION", "GO_CONTENT_GO_BACKUP_RETENTION"); ok {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return Config{}, fmt.Errorf("parse %s: %w", key, err)
			}
			backupRetention = parsed
		}
	}
	if backupRetention < 1 || backupRetention > 1000 {
		return Config{}, fmt.Errorf("backup retention must be between 1 and 1000 files")
	}

	trustedProxiesRaw := setting(flags, "trusted-proxies", *trustedProxiesFlag, dotenv, "GCG_TRUSTED_PROXIES", "GO_CONTENT_GO_TRUSTED_PROXIES")
	trustedProxies, err := parseTrustedProxyList(trustedProxiesRaw)
	if err != nil {
		return Config{}, fmt.Errorf("parse GCG_TRUSTED_PROXIES: %w", err)
	}

	return Config{
		Address:         address,
		DatabasePath:    databasePath,
		UploadPath:      uploadPath,
		MaxUploadSize:   maxUploadSize,
		ThemesPath:      themesPath,
		PluginsPath:     pluginsPath,
		BackupPath:      backupsPath,
		BackupRetention: backupRetention,
		APIRateLimit:    apiRateLimit,
		TrustedProxies:  trustedProxies,
	}, nil
}

func resolveAddress(flags *flag.FlagSet, flagValue string, dotenv map[string]string) string {
	if wasFlagSet(flags, "addr") {
		return flagValue
	}
	if value, _, ok := lookupSetting(dotenv, "GCG_ADDR", "GO_CONTENT_GO_ADDR"); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if port, _, ok := lookupSetting(dotenv, "GCG_PORT", "GO_CONTENT_GO_PORT"); ok {
		port = strings.TrimSpace(port)
		if port != "" {
			if strings.HasPrefix(port, ":") || strings.Contains(port, ":") {
				return port
			}
			return ":" + port
		}
	}
	return flagValue
}

func parseTrustedProxyList(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var proxies []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, err := netip.ParsePrefix(entry); err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", entry, err)
			}
		} else {
			if _, err := netip.ParseAddr(entry); err != nil {
				return nil, fmt.Errorf("invalid IP %q: %w", entry, err)
			}
		}
		proxies = append(proxies, entry)
	}
	return proxies, nil
}

func setting(flags *flag.FlagSet, flagName, flagValue string, dotenv map[string]string, envNames ...string) string {
	if wasFlagSet(flags, flagName) {
		return flagValue
	}
	if value, _, ok := lookupSetting(dotenv, envNames...); ok {
		return value
	}
	return flagValue
}

func lookupEnvAny(names ...string) (string, bool) {
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			return value, true
		}
	}
	return "", false
}

func lookupSetting(dotenv map[string]string, names ...string) (string, string, bool) {
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			return value, name, true
		}
	}
	for _, name := range names {
		if value, ok := dotenv[name]; ok {
			return value, name, true
		}
	}
	return "", "", false
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
