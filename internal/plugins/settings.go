package plugins

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const (
	maxSettingKeyLen   = 100
	maxSettingValueLen = 16 << 10
	maxPluginSettings  = 100
)

var (
	ErrInvalidSetting = errors.New("invalid plugin setting")
	settingKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,99}$`)
)

type AdminMenuItem struct {
	PluginID string
	Label    string
	Path     string
}

type AdminPageView struct {
	PluginID string
	Title    string
	HTML     string
}

func (m *Manager) SetDB(db *sql.DB) {
	m.mu.Lock()
	m.db = db
	m.mu.Unlock()
}

func (m *Manager) getDB() *sql.DB {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.db
}

func ValidSettingKey(key string) bool {
	return len(key) > 0 && len(key) <= maxSettingKeyLen && settingKeyPattern.MatchString(key)
}

func GetSetting(ctx context.Context, db *sql.DB, pluginID, key string) (string, bool, error) {
	if db == nil || !ValidID(pluginID) || !ValidSettingKey(key) {
		return "", false, ErrInvalidSetting
	}
	var value string
	err := db.QueryRowContext(ctx, `
		SELECT value FROM plugin_settings WHERE plugin_id = ? AND key = ?
	`, pluginID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read plugin setting: %w", err)
	}
	return value, true, nil
}

func SetSetting(ctx context.Context, db *sql.DB, pluginID, key, value string) error {
	if db == nil || !ValidID(pluginID) || !ValidSettingKey(key) || len(value) > maxSettingValueLen {
		return ErrInvalidSetting
	}
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM plugin_settings WHERE plugin_id = ? AND key != ?
	`, pluginID, key).Scan(&count); err != nil {
		return fmt.Errorf("count plugin settings: %w", err)
	}
	if count >= maxPluginSettings {
		return fmt.Errorf("%w: maximum settings count reached", ErrInvalidSetting)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO plugin_settings (plugin_id, key, value, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(plugin_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, pluginID, key, value, now); err != nil {
		return fmt.Errorf("save plugin setting: %w", err)
	}
	return nil
}

func AllSettings(ctx context.Context, db *sql.DB, pluginID string) (map[string]string, error) {
	if db == nil || !ValidID(pluginID) {
		return nil, ErrInvalidSetting
	}
	rows, err := db.QueryContext(ctx, `
		SELECT key, value FROM plugin_settings WHERE plugin_id = ? ORDER BY key
	`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("list plugin settings: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan plugin setting: %w", err)
		}
		result[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read plugin settings: %w", err)
	}
	return result, nil
}

func DeleteSetting(ctx context.Context, db *sql.DB, pluginID, key string) error {
	if db == nil || !ValidID(pluginID) || !ValidSettingKey(key) {
		return ErrInvalidSetting
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM plugin_settings WHERE plugin_id = ? AND key = ?
	`, pluginID, key); err != nil {
		return fmt.Errorf("delete plugin setting: %w", err)
	}
	return nil
}

// AdminMenus queries active plugins implementing admin:menu_register for sidebar items.
func (m *Manager) AdminMenus(ctx context.Context) []AdminMenuItem {
	listeners := m.listeners("admin:menu_register")
	if len(listeners) == 0 {
		return nil
	}
	items := make([]AdminMenuItem, 0, len(listeners))
	for _, plugin := range listeners {
		raw, err := m.run(ctx, plugin, "admin:menu_register", map[string]any{
			"plugin_id": plugin.manifest.ID,
		})
		if err != nil {
			log.Printf("plugin %q admin:menu_register failed: %v", plugin.manifest.ID, err)
			continue
		}
		label := plugin.manifest.Name
		if table, ok := raw.(map[string]any); ok {
			if customLabel, ok := table["label"].(string); ok && strings.TrimSpace(customLabel) != "" {
				label = strings.TrimSpace(customLabel)
				if len(label) > 60 {
					label = label[:60]
				}
			}
		} else if customLabel, ok := raw.(string); ok && strings.TrimSpace(customLabel) != "" {
			label = strings.TrimSpace(customLabel)
			if len(label) > 60 {
				label = label[:60]
			}
		}
		items = append(items, AdminMenuItem{
			PluginID: plugin.manifest.ID,
			Label:    label,
			Path:     "/admin/plugin/" + plugin.manifest.ID,
		})
	}
	return items
}

// RenderAdminPage executes the admin:page_register hook for a single active plugin.
func (m *Manager) RenderAdminPage(ctx context.Context, pluginID string) (AdminPageView, bool, error) {
	if !ValidID(pluginID) {
		return AdminPageView{}, false, nil
	}
	for _, plugin := range m.listeners("admin:page_register") {
		if plugin.manifest.ID != pluginID {
			continue
		}
		raw, err := m.run(ctx, plugin, "admin:page_register", map[string]any{
			"plugin_id": plugin.manifest.ID,
		})
		if err != nil {
			return AdminPageView{}, true, err
		}
		view := AdminPageView{
			PluginID: plugin.manifest.ID,
			Title:    plugin.manifest.Name,
		}
		switch typed := raw.(type) {
		case string:
			view.HTML = typed
		case map[string]any:
			if title, ok := typed["title"].(string); ok && strings.TrimSpace(title) != "" {
				view.Title = strings.TrimSpace(title)
			}
			if html, ok := typed["html"].(string); ok {
				view.HTML = html
			}
		}
		return view, true, nil
	}
	return AdminPageView{}, false, nil
}

// registerSettingsAPI injects an isolated `settings` Lua table bound strictly to pluginID.
func registerSettingsAPI(ctx context.Context, state *lua.LState, db *sql.DB, pluginID string) {
	tbl := state.NewTable()
	tbl.RawSetString("get", state.NewFunction(func(L *lua.LState) int {
		if db == nil {
			L.Push(lua.LNil)
			return 1
		}
		key := L.CheckString(1)
		val, found, err := GetSetting(ctx, db, pluginID, key)
		if err != nil || !found {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LString(val))
		return 1
	}))
	tbl.RawSetString("set", state.NewFunction(func(L *lua.LState) int {
		if db == nil {
			L.Push(lua.LBool(false))
			return 1
		}
		key := L.CheckString(1)
		val := L.CheckString(2)
		if err := SetSetting(ctx, db, pluginID, key, val); err != nil {
			L.Push(lua.LBool(false))
			return 1
		}
		L.Push(lua.LBool(true))
		return 1
	}))
	tbl.RawSetString("all", state.NewFunction(func(L *lua.LState) int {
		out := L.NewTable()
		if db == nil {
			L.Push(out)
			return 1
		}
		all, err := AllSettings(ctx, db, pluginID)
		if err != nil {
			L.Push(out)
			return 1
		}
		for k, v := range all {
			out.RawSetString(k, lua.LString(v))
		}
		L.Push(out)
		return 1
	}))
	tbl.RawSetString("delete", state.NewFunction(func(L *lua.LState) int {
		if db == nil {
			L.Push(lua.LBool(false))
			return 1
		}
		key := L.CheckString(1)
		if err := DeleteSetting(ctx, db, pluginID, key); err != nil {
			L.Push(lua.LBool(false))
			return 1
		}
		L.Push(lua.LBool(true))
		return 1
	}))
	state.SetGlobal("settings", tbl)
}
