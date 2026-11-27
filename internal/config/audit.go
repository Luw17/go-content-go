package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type PermissionFinding struct {
	Path    string
	Mode    string
	Status  string // "ok", "warning", "missing"
	Detail  string
	Fixable bool
}

// AuditTarget describes a filesystem path to check for safe permissions.
type AuditTarget struct {
	Label       string
	Path        string
	SensitiveDB bool
}

// AuditPermissions inspects filesystem permissions for key CMS paths.
// On POSIX systems, it flags world-writable directories/files and world-readable database files.
func AuditPermissions(targets []AuditTarget) []PermissionFinding {
	findings := make([]PermissionFinding, 0, len(targets))
	for _, target := range targets {
		if target.Path == "" {
			continue
		}
		cleanPath := filepath.Clean(target.Path)
		info, err := os.Lstat(cleanPath)
		if err != nil {
			if os.IsNotExist(err) {
				findings = append(findings, PermissionFinding{
					Path:    cleanPath,
					Status:  "missing",
					Detail:  fmt.Sprintf("%s ainda não existe no disco.", target.Label),
					Fixable: true,
				})
				continue
			}
			findings = append(findings, PermissionFinding{
				Path:   cleanPath,
				Status: "warning",
				Detail: fmt.Sprintf("Não foi possível inspecionar %s: %v", target.Label, err),
			})
			continue
		}
		mode := info.Mode()
		perm := mode.Perm()
		finding := PermissionFinding{
			Path:   cleanPath,
			Mode:   fmt.Sprintf("%04o", perm),
			Status: "ok",
			Detail: fmt.Sprintf("%s com permissões seguras.", target.Label),
		}
		if mode&os.ModeSymlink != 0 {
			finding.Status = "warning"
			finding.Detail = fmt.Sprintf("%s é um link simbólico; prefira caminho físico direto.", target.Label)
			findings = append(findings, finding)
			continue
		}
		if runtime.GOOS != "windows" {
			if perm&0o002 != 0 {
				finding.Status = "warning"
				finding.Fixable = true
				finding.Detail = fmt.Sprintf("%s tem permissão de escrita pública (%04o); remova o bit world-writable.", target.Label, perm)
			} else if target.SensitiveDB && perm&0o077 != 0 {
				finding.Status = "warning"
				finding.Fixable = true
				finding.Detail = fmt.Sprintf("%s é legível por grupo/outros (%04o); recomenda-se 0600.", target.Label, perm)
			}
		}
		findings = append(findings, finding)
	}
	return findings
}

// FixPermissions safely tightens permissions (or creates missing directories) for allowlisted CMS targets.
// Symlinks are skipped to avoid modifying unexpected targets.
func FixPermissions(targets []AuditTarget) (int, error) {
	fixed := 0
	var firstErr error
	for _, target := range targets {
		if target.Path == "" {
			continue
		}
		cleanPath := filepath.Clean(target.Path)
		info, err := os.Lstat(cleanPath)
		if err != nil {
			if os.IsNotExist(err) && !target.SensitiveDB {
				if mkErr := os.MkdirAll(cleanPath, 0o755); mkErr != nil {
					if firstErr == nil {
						firstErr = mkErr
					}
					continue
				}
				fixed++
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 || runtime.GOOS == "windows" {
			continue
		}
		perm := mode.Perm()
		if target.SensitiveDB && perm&0o077 != 0 {
			if chErr := os.Chmod(cleanPath, 0o600); chErr != nil {
				if firstErr == nil {
					firstErr = chErr
				}
				continue
			}
			fixed++
		} else if perm&0o002 != 0 {
			safePerm := perm &^ 0o022
			if chErr := os.Chmod(cleanPath, safePerm); chErr != nil {
				if firstErr == nil {
					firstErr = chErr
				}
				continue
			}
			fixed++
		}
	}
	return fixed, firstErr
}
