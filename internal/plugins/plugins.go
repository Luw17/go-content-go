package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const (
	DefaultTimeout = 100 * time.Millisecond
	maxManifest    = 16 << 10
	maxScript      = 256 << 10
	maxPlugins     = 20
)

var (
	ErrNotFound = errors.New("plugin not found")
	pluginID    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	hookSet     = map[string]bool{
		"post:before_save": true,
		"post:after_save":  true,
		"user:login":       true,
		"content:render":   true,
		"head:inject_tags": true,
	}
	filterHooks = map[string]bool{
		"content:render":   true,
		"head:inject_tags": true,
	}
)

type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	MainFile    string   `json:"main_file"`
	Hooks       []string `json:"hooks"`
	Active      bool     `json:"-"`
	Valid       bool     `json:"-"`
}

type loadedPlugin struct {
	manifest Manifest
	source   []byte
}

type Manager struct {
	root     string
	timeout  time.Duration
	updateMu sync.Mutex
	mu       sync.RWMutex
	active   map[string][]loadedPlugin
}

func New(root string, timeout time.Duration) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("plugins directory must not be empty")
	}
	if timeout <= 0 || timeout > time.Second {
		return nil, fmt.Errorf("plugin timeout must be between 1ns and 1s")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve plugins directory: %w", err)
	}
	return &Manager{root: filepath.Clean(absolute), timeout: timeout, active: make(map[string][]loadedPlugin)}, nil
}

func ValidID(id string) bool {
	return pluginID.MatchString(id)
}

func (m *Manager) List(ctx context.Context, db *sql.DB) ([]Manifest, error) {
	active, err := m.activeIDs(ctx, db)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.root)
	if errors.Is(err, os.ErrNotExist) {
		return []Manifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugins directory: %w", err)
	}
	result := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}
		manifest, err := m.readManifest(entry.Name())
		if err != nil {
			manifest = Manifest{ID: entry.Name(), Name: entry.Name()}
		} else {
			manifest.Valid = m.validateMainFile(manifest) == nil
		}
		manifest.Active = active[entry.Name()]
		result = append(result, manifest)
	}
	return result, nil
}

func (m *Manager) SetActive(ctx context.Context, db *sql.DB, id string, active bool) error {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	if !ValidID(id) {
		return ErrNotFound
	}
	ids, err := m.activeIDs(ctx, db)
	if err != nil {
		return err
	}
	if active {
		manifest, err := m.readManifest(id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrNotFound
			}
			return fmt.Errorf("validate plugin %q: %w", id, err)
		}
		source, err := m.readMainFile(manifest)
		if err != nil {
			return fmt.Errorf("validate plugin %q: %w", id, err)
		}
		if err := validatePluginModule(manifest, source, m.timeout); err != nil {
			return fmt.Errorf("validate plugin %q: %w", id, err)
		}
		ids[id] = true
	} else {
		delete(ids, id)
	}
	ordered := make([]string, 0, len(ids))
	for name := range ids {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	if len(ordered) > maxPlugins {
		return fmt.Errorf("active plugin limit exceeded")
	}
	next := m.loadPlugins(ordered)
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return fmt.Errorf("encode active plugins: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO options (key, value, updated_at)
		VALUES ('active_plugins', ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, string(encoded)); err != nil {
		return fmt.Errorf("save active plugins: %w", err)
	}
	m.mu.Lock()
	m.active = next
	m.mu.Unlock()
	return nil
}

func (m *Manager) Reload(ctx context.Context, db *sql.DB) error {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	ids, err := m.activeIDs(ctx, db)
	if err != nil {
		return err
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	slices.Sort(ordered)
	next := m.loadPlugins(ordered)
	m.mu.Lock()
	m.active = next
	m.mu.Unlock()
	return nil
}

func (m *Manager) activeIDs(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM options WHERE key = 'active_plugins'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read active plugins: %w", err)
	}
	if len(raw) > maxPlugins*66 {
		return nil, fmt.Errorf("active plugins option exceeds the size limit")
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("decode active plugins: %w", err)
	}
	if len(ids) > maxPlugins {
		return nil, fmt.Errorf("active plugin limit exceeded")
	}
	active := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !ValidID(id) || active[id] {
			return nil, fmt.Errorf("invalid or duplicate active plugin ID %q", id)
		}
		active[id] = true
	}
	return active, nil
}

func (m *Manager) loadPlugins(ids []string) map[string][]loadedPlugin {
	next := make(map[string][]loadedPlugin)
	for _, id := range ids {
		manifest, err := m.readManifest(id)
		if err != nil {
			log.Printf("skip active plugin %q: %v", id, err)
			continue
		}
		source, err := m.readMainFile(manifest)
		if err != nil {
			log.Printf("skip active plugin %q: %v", id, err)
			continue
		}
		if err := validatePluginModule(manifest, source, m.timeout); err != nil {
			log.Printf("skip active plugin %q: %v", id, err)
			continue
		}
		plugin := loadedPlugin{manifest: manifest, source: source}
		for _, hook := range manifest.Hooks {
			next[hook] = append(next[hook], plugin)
		}
	}
	return next
}

func (m *Manager) readManifest(id string) (Manifest, error) {
	directory, err := m.pluginDirectory(id)
	if err != nil {
		return Manifest{}, err
	}
	path := filepath.Join(directory, "plugin.json")
	info, err := os.Lstat(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect plugin manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxManifest {
		return Manifest{}, fmt.Errorf("plugin manifest must be a regular file no larger than 16 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open plugin manifest: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxManifest+1))
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse plugin manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("plugin manifest contains trailing JSON")
	}
	if manifest.ID != id || strings.TrimSpace(manifest.Name) == "" ||
		len(manifest.Name) > 120 || len(manifest.Version) > 64 ||
		len(manifest.Description) > 1000 || len(manifest.Author) > 120 ||
		manifest.MainFile == "" || filepath.Base(manifest.MainFile) != manifest.MainFile ||
		filepath.Ext(manifest.MainFile) != ".lua" || len(manifest.Hooks) == 0 || len(manifest.Hooks) > len(hookSet) {
		return Manifest{}, fmt.Errorf("plugin manifest has invalid required fields")
	}
	seen := make(map[string]bool, len(manifest.Hooks))
	for _, hook := range manifest.Hooks {
		if !hookSet[hook] || seen[hook] {
			return Manifest{}, fmt.Errorf("plugin manifest has an unsupported or duplicate hook %q", hook)
		}
		seen[hook] = true
	}
	manifest.Valid = true
	return manifest, nil
}

func (m *Manager) validateMainFile(manifest Manifest) error {
	directory, err := m.pluginDirectory(manifest.ID)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, manifest.MainFile)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect plugin main file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxScript {
		return fmt.Errorf("plugin main file must be a regular file no larger than 256 KiB")
	}
	return nil
}

func (m *Manager) readMainFile(manifest Manifest) ([]byte, error) {
	directory, err := m.pluginDirectory(manifest.ID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, manifest.MainFile)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect plugin main file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxScript {
		return nil, fmt.Errorf("plugin main file must be a regular file no larger than 256 KiB")
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin main file: %w", err)
	}
	return source, nil
}

func (m *Manager) pluginDirectory(id string) (string, error) {
	if !ValidID(id) {
		return "", ErrNotFound
	}
	path := filepath.Join(m.root, id)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("inspect plugin directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrNotFound
	}
	return path, nil
}

func (m *Manager) DispatchAction(ctx context.Context, hook string, value any) {
	if filterHooks[hook] || !hookSet[hook] {
		log.Printf("refusing invalid action hook %q", hook)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	for _, plugin := range m.listeners(hook) {
		if _, err := m.run(ctx, plugin, hook, value); err != nil {
			log.Printf("plugin %q action %s failed: %v", plugin.manifest.ID, hook, err)
		}
	}
}

func (m *Manager) ApplyFilters(ctx context.Context, hook string, value any) any {
	if !filterHooks[hook] {
		log.Printf("refusing invalid filter hook %q", hook)
		return value
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	result := value
	for _, plugin := range m.listeners(hook) {
		filtered, err := m.run(ctx, plugin, hook, result)
		if err != nil {
			log.Printf("plugin %q filter %s failed; preserving previous value: %v", plugin.manifest.ID, hook, err)
			continue
		}
		if filtered != nil {
			result = filtered
		}
	}
	return result
}

func (m *Manager) listeners(hook string) []loadedPlugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]loadedPlugin(nil), m.active[hook]...)
}

func (m *Manager) run(parent context.Context, plugin loadedPlugin, hook string, value any) (result any, runErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("plugin panic recovered: %v", recovered)
		}
	}()
	ctx, cancel := context.WithTimeout(parent, m.timeout)
	defer cancel()
	state := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer state.Close()
	state.SetContext(ctx)
	openSafeLibraries(state)
	chunk, err := state.Load(strings.NewReader(string(plugin.source)), plugin.manifest.MainFile)
	if err != nil {
		return nil, err
	}
	if err := state.CallByParam(lua.P{Fn: chunk, NRet: 1, Protect: true}); err != nil {
		return nil, err
	}
	module := state.Get(-1)
	state.Pop(1)
	moduleTable, ok := module.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("plugin must return a module table")
	}
	hooksTable, ok := moduleTable.RawGetString("hooks").(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("plugin module must contain a hooks table")
	}
	callback, ok := hooksTable.RawGetString(hook).(*lua.LFunction)
	if !ok {
		return nil, fmt.Errorf("plugin hook %q is not a function", hook)
	}
	luaValue, err := pushValue(state, value)
	if err != nil {
		return nil, err
	}
	if err := state.CallByParam(lua.P{Fn: callback, NRet: 1, Protect: true}, luaValue); err != nil {
		return nil, err
	}
	return fromLua(ctx, state.Get(-1), 0, new(int))
}

func validatePluginModule(manifest Manifest, source []byte, timeout time.Duration) (validationErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			validationErr = fmt.Errorf("plugin initialization panic recovered: %v", recovered)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	state := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer state.Close()
	state.SetContext(ctx)
	openSafeLibraries(state)
	chunk, err := state.Load(strings.NewReader(string(source)), manifest.MainFile)
	if err != nil {
		return fmt.Errorf("compile plugin script: %w", err)
	}
	if err := state.CallByParam(lua.P{Fn: chunk, NRet: 1, Protect: true}); err != nil {
		return fmt.Errorf("initialize plugin module: %w", err)
	}
	module, ok := state.Get(-1).(*lua.LTable)
	if !ok {
		return fmt.Errorf("plugin must return a module table")
	}
	hooks, ok := module.RawGetString("hooks").(*lua.LTable)
	if !ok {
		return fmt.Errorf("plugin module must contain a hooks table")
	}
	for _, hook := range manifest.Hooks {
		if _, ok := hooks.RawGetString(hook).(*lua.LFunction); !ok {
			return fmt.Errorf("plugin hook %q is not a function", hook)
		}
	}
	return nil
}

func openSafeLibraries(state *lua.LState) {
	lua.OpenBase(state)
	lua.OpenTable(state)
	lua.OpenString(state)
	lua.OpenMath(state)
	for _, name := range []string{"dofile", "loadfile", "load", "collectgarbage", "print", "rawset"} {
		state.SetGlobal(name, lua.LNil)
	}
	stringLibrary := state.GetGlobal("string")
	if library, ok := stringLibrary.(*lua.LTable); ok {
		library.RawSetString("rep", lua.LNil)
		library.RawSetString("format", lua.LNil)
	}
}

func pushValue(state *lua.LState, value any) (lua.LValue, error) {
	switch typed := value.(type) {
	case nil:
		return lua.LNil, nil
	case string:
		return lua.LString(typed), nil
	case bool:
		return lua.LBool(typed), nil
	case int:
		return lua.LNumber(typed), nil
	case int64:
		return lua.LNumber(typed), nil
	case float64:
		return lua.LNumber(typed), nil
	case map[string]any:
		result := state.NewTable()
		for key, element := range typed {
			luaElement, err := pushValue(state, element)
			if err != nil {
				return nil, err
			}
			result.RawSetString(key, luaElement)
		}
		return result, nil
	case []any:
		result := state.NewTable()
		for _, element := range typed {
			luaElement, err := pushValue(state, element)
			if err != nil {
				return nil, err
			}
			result.Append(luaElement)
		}
		return result, nil
	case []int64:
		result := state.NewTable()
		for _, element := range typed {
			result.Append(lua.LNumber(element))
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported plugin input type %T", value)
	}
}

func fromLua(ctx context.Context, value lua.LValue, depth int, nodes *int) (any, error) {
	if depth > 16 {
		return nil, fmt.Errorf("plugin result exceeds maximum nesting")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch typed := value.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LString:
		return string(typed), nil
	case lua.LBool:
		return bool(typed), nil
	case lua.LNumber:
		return float64(typed), nil
	case *lua.LTable:
		result := make(map[string]any)
		var conversionErr error
		typed.ForEach(func(key, element lua.LValue) {
			if conversionErr != nil {
				return
			}
			*nodes = *nodes + 1
			if *nodes > 4096 {
				conversionErr = fmt.Errorf("plugin result exceeds maximum table size")
				return
			}
			if err := ctx.Err(); err != nil {
				conversionErr = err
				return
			}
			keyString, ok := key.(lua.LString)
			if !ok {
				conversionErr = fmt.Errorf("plugin result table keys must be strings")
				return
			}
			converted, err := fromLua(ctx, element, depth+1, nodes)
			if err != nil {
				conversionErr = err
				return
			}
			result[string(keyString)] = converted
		})
		if conversionErr != nil {
			return nil, conversionErr
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported plugin result type %s", value.Type())
	}
}
