package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
)

//go:embed templates/*.html static/*
var files embed.FS

// Templates returns the embedded HTML templates.
func Templates() (fs.FS, error) {
	templates, err := fs.Sub(files, "templates")
	if err != nil {
		return nil, fmt.Errorf("open embedded templates: %w", err)
	}
	return templates, nil
}

// StaticFiles returns the embedded static assets.
func StaticFiles() (fs.FS, error) {
	static, err := fs.Sub(files, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded static files: %w", err)
	}
	return static, nil
}

// StaticVersion returns a content hash suitable for cache-busting asset URLs.
func StaticVersion(filename string) (string, error) {
	static, err := StaticFiles()
	if err != nil {
		return "", err
	}
	content, err := fs.ReadFile(static, filename)
	if err != nil {
		return "", fmt.Errorf("read embedded static asset %q: %w", filename, err)
	}
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:]), nil
}
