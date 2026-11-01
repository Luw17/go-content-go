package packagezip

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	MaxArchiveSize    = int64(20 << 20)
	maxExpandedSize   = uint64(100 << 20)
	maxSingleFileSize = uint64(32 << 20)
	maxArchiveEntries = 2048
)

var (
	ErrInvalidPackage = errors.New("invalid package archive")
	ErrTooLarge       = errors.New("package archive exceeds size limit")
	ErrAlreadyExists  = errors.New("package is already installed")
)

type IdentifyFunc func(directory, archiveName, packageDirectory string) (string, error)
type ValidateFunc func(directory, packageID string) error

func InstallZip(source io.Reader, archiveName, targetRoot string, identify IdentifyFunc, validate ValidateFunc) (string, error) {
	if source == nil || identify == nil || validate == nil || strings.TrimSpace(targetRoot) == "" {
		return "", ErrInvalidPackage
	}
	if strings.ToLower(filepath.Ext(filepath.Base(archiveName))) != ".zip" {
		return "", ErrInvalidPackage
	}
	if err := os.MkdirAll(targetRoot, 0750); err != nil {
		return "", fmt.Errorf("create package directory: %w", err)
	}
	root, err := filepath.Abs(targetRoot)
	if err != nil {
		return "", fmt.Errorf("resolve package directory: %w", err)
	}
	archiveFile, err := os.CreateTemp("", "gocg-package-*.zip")
	if err != nil {
		return "", fmt.Errorf("create temporary package archive: %w", err)
	}
	archivePath := archiveFile.Name()
	defer func() {
		if err := os.Remove(archivePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("remove temporary package archive: %v", err)
		}
	}()
	size, err := io.Copy(archiveFile, io.LimitReader(source, MaxArchiveSize+1))
	if err != nil {
		_ = archiveFile.Close()
		return "", fmt.Errorf("write temporary package archive: %w", err)
	}
	if size > MaxArchiveSize {
		_ = archiveFile.Close()
		return "", ErrTooLarge
	}
	if size == 0 {
		_ = archiveFile.Close()
		return "", ErrInvalidPackage
	}
	if err := archiveFile.Sync(); err != nil {
		_ = archiveFile.Close()
		return "", fmt.Errorf("flush temporary package archive: %w", err)
	}
	if err := archiveFile.Close(); err != nil {
		return "", fmt.Errorf("close temporary package archive: %w", err)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	defer reader.Close()
	if len(reader.File) == 0 || len(reader.File) > maxArchiveEntries {
		return "", ErrInvalidPackage
	}

	stage, err := os.MkdirTemp(root, ".install-*")
	if err != nil {
		return "", fmt.Errorf("create temporary package directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			log.Printf("remove temporary package directory: %v", err)
		}
	}()
	if err := extract(reader.File, stage); err != nil {
		return "", err
	}

	packageDir, packageDirectory := packageRoot(reader.File, stage)
	packageID, err := identify(packageDir, filepath.Base(archiveName), packageDirectory)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	if packageID == "" || packageID == "." || !fs.ValidPath(packageID) ||
		path.Clean(packageID) != packageID || filepath.IsAbs(packageID) ||
		strings.ContainsAny(packageID, "/\\:\x00") {
		return "", ErrInvalidPackage
	}
	if err := validate(packageDir, packageID); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	destination := filepath.Join(root, packageID)
	if _, err := os.Lstat(destination); err == nil {
		return "", ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect package destination: %w", err)
	}
	if err := os.Rename(packageDir, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", ErrAlreadyExists
		}
		return "", fmt.Errorf("publish package: %w", err)
	}
	return packageID, nil
}

func extract(entries []*zip.File, stage string) error {
	seen := make(map[string]bool, len(entries))
	var expanded uint64
	for _, entry := range entries {
		name, directory, err := safeEntryName(entry)
		if err != nil || seen[name] {
			return ErrInvalidPackage
		}
		seen[name] = true
		if entry.UncompressedSize64 > maxSingleFileSize ||
			entry.UncompressedSize64 > maxExpandedSize-expanded {
			return ErrInvalidPackage
		}
		expanded += entry.UncompressedSize64

		destination := filepath.Join(stage, filepath.FromSlash(name))
		relative, err := filepath.Rel(stage, destination)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ErrInvalidPackage
		}
		if directory {
			if err := os.MkdirAll(destination, 0750); err != nil {
				return fmt.Errorf("create package directory entry: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
			return fmt.Errorf("create package parent directory: %w", err)
		}
		input, err := entry.Open()
		if err != nil {
			return fmt.Errorf("%w: cannot read archive entry", ErrInvalidPackage)
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
		if err != nil {
			_ = input.Close()
			return fmt.Errorf("%w: duplicate or invalid archive entry", ErrInvalidPackage)
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, int64(maxSingleFileSize)+1))
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil || inputErr != nil || outputErr != nil ||
			written > int64(maxSingleFileSize) || uint64(written) != entry.UncompressedSize64 {
			return fmt.Errorf("%w: archive entry is corrupt or exceeds limits", ErrInvalidPackage)
		}
	}
	return nil
}

func safeEntryName(entry *zip.File) (string, bool, error) {
	raw := entry.Name
	if raw == "" || strings.ContainsAny(raw, "\\:\x00") || strings.HasPrefix(raw, "/") ||
		len(raw) > 4096 || filepath.IsAbs(raw) {
		return "", false, ErrInvalidPackage
	}
	directory := entry.FileInfo().IsDir() || strings.HasSuffix(raw, "/")
	if strings.HasSuffix(raw, "/") {
		raw = strings.TrimSuffix(raw, "/")
	}
	if !fs.ValidPath(raw) || raw == "." || path.Clean(raw) != raw {
		return "", false, ErrInvalidPackage
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false, ErrInvalidPackage
		}
	}
	mode := entry.Mode()
	if mode&os.ModeSymlink != 0 || (mode.Type() != 0 && !mode.IsRegular() && !mode.IsDir()) {
		return "", false, ErrInvalidPackage
	}
	if directory && !entry.FileInfo().IsDir() && mode.Type() != 0 && !mode.IsDir() {
		return "", false, ErrInvalidPackage
	}
	return raw, directory, nil
}

func packageRoot(entries []*zip.File, stage string) (string, string) {
	common := ""
	wrapped := true
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name, "/")
		parts := strings.Split(name, "/")
		if len(parts) < 2 {
			if entry.FileInfo().IsDir() && common == "" {
				common = parts[0]
				continue
			}
			wrapped = false
			break
		}
		if common == "" {
			common = parts[0]
		} else if common != parts[0] {
			wrapped = false
			break
		}
	}
	if wrapped && common != "" {
		return filepath.Join(stage, filepath.FromSlash(common)), common
	}
	return stage, ""
}
