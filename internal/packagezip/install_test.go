package packagezip

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallZipExtractsWrappedPackageAndRejectsExistingDestination(t *testing.T) {
	root := t.TempDir()
	archive := makeZip(t, map[string]string{
		"sample/main.txt": "package contents",
	})
	identify := func(_, _, packageDirectory string) (string, error) {
		if packageDirectory != "sample" {
			t.Fatalf("package directory = %q, want sample", packageDirectory)
		}
		return packageDirectory, nil
	}
	validate := func(directory, id string) error {
		if id != "sample" {
			t.Fatalf("package ID = %q, want sample", id)
		}
		contents, err := os.ReadFile(filepath.Join(directory, "main.txt"))
		if err != nil {
			return err
		}
		if string(contents) != "package contents" {
			t.Fatalf("package contents = %q", contents)
		}
		return nil
	}
	installed, err := InstallZip(bytes.NewReader(archive), "sample.zip", root, identify, validate)
	if err != nil {
		t.Fatal(err)
	}
	if installed != "sample" {
		t.Fatalf("installed ID = %q, want sample", installed)
	}
	if _, err := os.Stat(filepath.Join(root, "sample", "main.txt")); err != nil {
		t.Fatalf("installed file is missing: %v", err)
	}
	if _, err := InstallZip(bytes.NewReader(archive), "sample.zip", root, identify, validate); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("install over existing package error = %v, want ErrAlreadyExists", err)
	}
}

func TestInstallZipRejectsTraversalAndCleansStagingDirectory(t *testing.T) {
	root := t.TempDir()
	archive := makeZip(t, map[string]string{
		"../escape.txt": "outside",
		"safe.txt":      "inside",
	})
	_, err := InstallZip(bytes.NewReader(archive), "unsafe.zip", root,
		func(_, _, _ string) (string, error) { return "unsafe", nil },
		func(_, _ string) error { return nil },
	)
	if !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("install traversal package error = %v, want ErrInvalidPackage", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("traversal wrote outside the package root: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging files were not cleaned up: %v", entries)
	}
}

func TestInstallZipRejectsInvalidPackageIdentifier(t *testing.T) {
	root := t.TempDir()
	archive := makeZip(t, map[string]string{"safe.txt": "contents"})
	_, err := InstallZip(bytes.NewReader(archive), "unsafe.zip", root,
		func(_, _, _ string) (string, error) { return "../outside", nil },
		func(_, _ string) error { return nil },
	)
	if !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("invalid package ID error = %v, want ErrInvalidPackage", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid package ID escaped the package root: %v", err)
	}
}

func TestInstallZipRejectsInvalidArchiveAndOversizedInput(t *testing.T) {
	root := t.TempDir()
	identify := func(_, _, _ string) (string, error) { return "sample", nil }
	validate := func(_, _ string) error { return nil }
	if _, err := InstallZip(bytes.NewReader([]byte("not zip")), "sample.zip", root, identify, validate); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("invalid archive error = %v, want ErrInvalidPackage", err)
	}
	oversized := io.LimitReader(zeroReader{}, MaxArchiveSize+1)
	if _, err := InstallZip(oversized, "sample.zip", root, identify, validate); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized archive error = %v, want ErrTooLarge", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for filename, contents := range files {
		entry, err := writer.Create(filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}
