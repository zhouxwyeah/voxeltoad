package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"voxeltoad/internal/config"
)

// ConfigSnapshotRepo manages persisted config snapshots used for version
// history, diff, rollback, and dry-run previews. Snapshots are stored after
// every config mutation (fail-open, non-pessimistic — best-effort save).
type ConfigSnapshotRepo struct {
	db *DB
}

// NewConfigSnapshotRepo builds a ConfigSnapshotRepo over the given connection.
func NewConfigSnapshotRepo(db *DB) *ConfigSnapshotRepo {
	return &ConfigSnapshotRepo{db: db}
}

// SaveSnapshot persists a config.Dynamic snapshot under the given version.
// ON CONFLICT DO NOTHING makes it idempotent if the same version is saved
// concurrently.
func (r *ConfigSnapshotRepo) SaveSnapshot(ctx context.Context, version int64, d *config.Dynamic) error {
	payload, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO config_snapshots (version, payload, created_at) VALUES (?, ?, now())
		 ON CONFLICT (version) DO NOTHING`, version, payload,
	).Error
}

// SnapshotItem is a lightweight row for listing — excludes the full payload.
type SnapshotItem struct {
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

// ListSnapshots returns snapshots in descending version order with keyset
// pagination (cursor is a base64-encoded version). limit <=0 or >100 caps at
// 50.
func (r *ConfigSnapshotRepo) ListSnapshots(ctx context.Context, cursor string, limit int) ([]SnapshotItem, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT version, created_at FROM config_snapshots ORDER BY version DESC LIMIT ?`
	var rows []SnapshotItem
	var err error
	if cursor != "" {
		cursorVersion, decErr := decodeSnapshotCursor(cursor)
		if decErr != nil {
			return nil, "", decErr
		}
		query = `SELECT version, created_at FROM config_snapshots WHERE version < ? ORDER BY version DESC LIMIT ?`
		err = r.db.WithContext(ctx).Raw(query, cursorVersion, limit+1).Scan(&rows).Error
	} else {
		err = r.db.WithContext(ctx).Raw(query, limit+1).Scan(&rows).Error
	}
	if err != nil {
		return nil, "", err
	}
	hasNext := len(rows) > limit
	if hasNext {
		rows = rows[:limit]
	}
	next := ""
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = encodeSnapshotCursor(last.Version)
	}
	return rows, next, nil
}

// Get returns the full snapshot payload at a specific version.
func (r *ConfigSnapshotRepo) Get(ctx context.Context, version int64) (*config.Dynamic, error) {
	var raw string
	if err := r.db.WithContext(ctx).Raw(
		`SELECT payload::text FROM config_snapshots WHERE version = ?`, version,
	).Scan(&raw).Error; err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var d config.Dynamic
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("config_snapshots: unmarshal version %d: %w", version, err)
	}
	return &d, nil
}

// GetCurrentGeneration returns the current config_generation value.
func (r *ConfigSnapshotRepo) GetCurrentGeneration(ctx context.Context) (int64, error) {
	var v int64
	if err := r.db.WithContext(ctx).Raw(`SELECT version FROM config_generation`).Scan(&v).Error; err != nil {
		return 0, err
	}
	return v, nil
}

// Rollback replaces all current config tables with the content from the given
// snapshot version, then bumps generation.  The data-plane poller naturally
// picks up the new generation as a normal update.
func (r *ConfigSnapshotRepo) Rollback(ctx context.Context, version int64) error {
	d, err := r.Get(ctx, version)
	if err != nil {
		return err
	}
	if d == nil {
		return fmt.Errorf("config_snapshots: version %d not found", version)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM providers`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM models`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM routes`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM plugins`).Error; err != nil {
			return err
		}
		for _, p := range d.Providers {
			spec, merr := json.Marshal(p)
			if merr != nil {
				return merr
			}
			if err := tx.Exec(
				`INSERT INTO providers (name, type, adapter, enabled, spec, updated_at)
				 VALUES (?, ?, ?, true, ?, now())`,
				p.Name, p.Type, p.PrimaryAdapter(), spec,
			).Error; err != nil {
				return err
			}
		}
		for _, m := range d.Models {
			spec, merr := json.Marshal(m)
			if merr != nil {
				return merr
			}
			if err := tx.Exec(
				`INSERT INTO models (alias, enabled, spec, updated_at)
				 VALUES (?, true, ?, now())`,
				m.Alias, spec,
			).Error; err != nil {
				return err
			}
		}
		for _, rt := range d.Routes {
			spec, merr := json.Marshal(rt)
			if merr != nil {
				return merr
			}
			if err := tx.Exec(
				`INSERT INTO routes (model_alias, strategy, enabled, spec, updated_at)
				 VALUES (?, ?, true, ?, now())`,
				rt.ModelAlias, rt.Strategy, spec,
			).Error; err != nil {
				return err
			}
		}
		for _, pc := range d.Plugins {
			spec, merr := json.Marshal(pc)
			if merr != nil {
				return merr
			}
			if err := tx.Exec(
				`INSERT INTO plugins (name, phase, scope, enabled, spec, updated_at)
				 VALUES (?, ?, ?, ?, ?, now())`,
				pc.Name, pc.Phase, pc.Scope, pc.Enabled, spec,
			).Error; err != nil {
				return err
			}
		}
		// Restore gateway_settings from the snapshot so a rollback yields a fully
		// consistent restore (ADR-0025). Snapshots written before settings existed
		// have Settings == nil — leave the live settings untouched (safe degrade).
		if d.Settings != nil {
			spec, merr := json.Marshal(d.Settings)
			if merr != nil {
				return merr
			}
			if err := tx.Exec(
				`UPDATE gateway_settings SET spec = ?, updated_at = now() WHERE id = 1`,
				json.RawMessage(spec),
			).Error; err != nil {
				return err
			}
		}
		return bumpGeneration(tx)
	})
}

// Diff returns a human-readable summary of changes between two snapshot
// versions. Returns an error if either version is missing.
func (r *ConfigSnapshotRepo) Diff(ctx context.Context, fromVersion, toVersion int64) (*ConfigDiff, error) {
	from, err := r.Get(ctx, fromVersion)
	if err != nil || from == nil {
		return nil, fmt.Errorf("source version %d not found", fromVersion)
	}
	to, err := r.Get(ctx, toVersion)
	if err != nil || to == nil {
		return nil, fmt.Errorf("target version %d not found", toVersion)
	}
	diff := &ConfigDiff{
		FromVersion: fromVersion,
		ToVersion:   toVersion,
	}
	// Providers
	fromProv := setBy(from.Providers, func(p config.Provider) string { return p.Name })
	toProv := setBy(to.Providers, func(p config.Provider) string { return p.Name })
	for name := range fromProv {
		if _, ok := toProv[name]; !ok {
			diff.DeletedProviders = append(diff.DeletedProviders, name)
		}
	}
	for name := range toProv {
		if _, ok := fromProv[name]; !ok {
			diff.AddedProviders = append(diff.AddedProviders, name)
		}
	}
	// Models
	fromMod := setBy(from.Models, func(m config.Model) string { return m.Alias })
	toMod := setBy(to.Models, func(m config.Model) string { return m.Alias })
	for alias := range fromMod {
		if _, ok := toMod[alias]; !ok {
			diff.DeletedModels = append(diff.DeletedModels, alias)
		}
	}
	for alias := range toMod {
		if _, ok := fromMod[alias]; !ok {
			diff.AddedModels = append(diff.AddedModels, alias)
		}
	}
	// Routes
	fromRt := setBy(from.Routes, func(rt config.Route) string { return rt.ModelAlias })
	toRt := setBy(to.Routes, func(rt config.Route) string { return rt.ModelAlias })
	for ma := range fromRt {
		if _, ok := toRt[ma]; !ok {
			diff.DeletedRoutes = append(diff.DeletedRoutes, ma)
		}
	}
	for ma := range toRt {
		if _, ok := fromRt[ma]; !ok {
			diff.AddedRoutes = append(diff.AddedRoutes, ma)
		}
	}
	// Plugins
	fromPl := setBy(from.Plugins, func(pc config.PluginConfig) string { return pc.Name + "/" + pc.Scope })
	toPl := setBy(to.Plugins, func(pc config.PluginConfig) string { return pc.Name + "/" + pc.Scope })
	for k := range fromPl {
		if _, ok := toPl[k]; !ok {
			diff.DeletedPlugins = append(diff.DeletedPlugins, k)
		}
	}
	for k := range toPl {
		if _, ok := fromPl[k]; !ok {
			diff.AddedPlugins = append(diff.AddedPlugins, k)
		}
	}
	return diff, nil
}

// ConfigDiff is a structured summary of changes between two config snapshots.
type ConfigDiff struct {
	FromVersion int64 `json:"from_version"`
	ToVersion   int64 `json:"to_version"`
	// Added means the resource was added in toVersion compared to fromVersion
	AddedProviders []string `json:"added_providers,omitempty"`
	AddedModels    []string `json:"added_models,omitempty"`
	AddedRoutes    []string `json:"added_routes,omitempty"`
	AddedPlugins   []string `json:"added_plugins,omitempty"`
	// Deleted means the resource was removed in toVersion
	DeletedProviders []string `json:"deleted_providers,omitempty"`
	DeletedModels    []string `json:"deleted_models,omitempty"`
	DeletedRoutes    []string `json:"deleted_routes,omitempty"`
	DeletedPlugins   []string `json:"deleted_plugins,omitempty"`
}

func setBy[K comparable, V any](slice []V, keyFn func(V) K) map[K]struct{} {
	m := make(map[K]struct{}, len(slice))
	for _, v := range slice {
		m[keyFn(v)] = struct{}{}
	}
	return m
}

// --- cursor helpers ---

func decodeSnapshotCursor(c string) (int64, error) {
	raw, err := base64.StdEncoding.DecodeString(c)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor: %w", err)
	}
	var v int64
	if _, err := fmt.Sscanf(string(raw), "%d", &v); err != nil {
		return 0, fmt.Errorf("invalid cursor payload: %w", err)
	}
	return v, nil
}

func encodeSnapshotCursor(v int64) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", v)))
}
