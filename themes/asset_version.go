package themes

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// AssetVersion returns a short content hash for a theme asset to enable cache-busting (?v=hash).
func (m *Manager) AssetVersion(themeName, assetPath string) string {
	file, err := m.OpenAsset(themeName, assetPath)
	if err != nil {
		return ""
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, io.LimitReader(file, 4<<20)); err != nil {
		return ""
	}
	return hex.EncodeToString(hasher.Sum(nil))[:12]
}
