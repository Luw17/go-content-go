package media

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
)

// ObjectStorage defines a pluggable storage backend (local filesystem, S3, or Cloudflare R2).
type ObjectStorage interface {
	Put(ctx context.Context, key string, contentType string, body io.Reader) (publicURL string, err error)
	Delete(ctx context.Context, key string) error
}

const (
	maxThumbSourceDimension = 8192
	maxThumbSourcePixels    = 25_000_000
)

func generateThumbnail(srcPath, dstPath string, maxWidth int) error {
	if maxWidth < 32 {
		maxWidth = 480
	}
	file, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer file.Close()

	cfg, _, err := image.DecodeConfig(file)
	if err != nil {
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxThumbSourceDimension || cfg.Height > maxThumbSourceDimension ||
		int64(cfg.Width)*int64(cfg.Height) > maxThumbSourcePixels {
		return fmt.Errorf("image dimensions %dx%d exceed thumbnail limit", cfg.Width, cfg.Height)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	src, _, err := image.Decode(file)
	if err != nil {
		return err
	}
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return fmt.Errorf("empty image dimensions")
	}

	dstW := srcW
	dstH := srcH
	if srcW > maxWidth {
		dstW = maxWidth
		dstH = (srcH * maxWidth) / srcW
		if dstH < 1 {
			dstH = 1
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		sy := bounds.Min.Y + (y*srcH)/dstH
		for x := 0; x < dstW; x++ {
			sx := bounds.Min.X + (x*srcW)/dstW
			dst.Set(x, y, src.At(sx, sy))
		}
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(out, dst, &jpeg.Options{Quality: 82}); err != nil {
		_ = out.Close()
		_ = os.Remove(dstPath)
		return err
	}
	return out.Close()
}
