package web

import (
	"embed"
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
