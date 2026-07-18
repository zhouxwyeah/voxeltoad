package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	"gorm.io/gorm"

	"voxeltoad/internal/config"
)

// ConfigRepo is the management-plane store for the dynamic config resources
// (providers/models/routes/plugins). Each resource is persisted as its whole
// marshaled struct in a JSONB `spec` column plus denormalized identity columns
// for UNIQUE/queries (ADR-0014). Every mutation bumps the config_generation
// counter in the same transaction so the snapshot version is monotonic
// (ADR-0015). Snapshot assembles a config.Dynamic the data plane polls.
type ConfigRepo struct {
	db   *DB
	snap *ConfigSnapshotRepo
}

// NewConfigRepo builds a ConfigRepo over the given connection. The optional
// snapshot callback is fired asynchronously (fail-open) after every config
// mutation to persist a historical snapshot for rollback/diff.
func NewConfigRepo(db *DB, snap *ConfigSnapshotRepo) *ConfigRepo {
	return &ConfigRepo{db: db, snap: snap}
}

// bumpGeneration increments the single config_generation row. Call inside the
// same transaction as a config write so the version tracks content changes.
func bumpGeneration(tx *gorm.DB) error {
	return tx.Exec(`UPDATE config_generation SET version = version + 1`).Error
}

// saveAfterMutation fires an asynchronous best-effort snapshot save if the
// mutation succeeded and a snapshot repo is configured. Failures are logged
// but never returned — snapshot history is non-critical.
func (r *ConfigRepo) saveAfterMutation(ctx context.Context, mutationErr error) {
	if mutationErr != nil || r.snap == nil {
		return
	}
	go func() {
		d, err := r.Snapshot(context.Background())
		if err != nil {
			log.Printf("config: snapshot build failed: %v", err)
			return
		}
		v, _ := strconv.ParseInt(d.Version, 10, 64)
		if err := r.snap.SaveSnapshot(context.Background(), v, d); err != nil {
			log.Printf("config: save snapshot v%d failed: %v", v, err)
		}
	}()
}

// --- Providers ---

// UpsertProvider inserts or replaces a provider by name.
func (r *ConfigRepo) UpsertProvider(ctx context.Context, p config.Provider) error {
	spec, err := json.Marshal(p)
	if err != nil {
		return err
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`INSERT INTO providers (name, type, adapter, enabled, spec, updated_at)
			 VALUES (?, ?, ?, true, ?, now())
			 ON CONFLICT (name) DO UPDATE SET type = EXCLUDED.type,
			     adapter = EXCLUDED.adapter, spec = EXCLUDED.spec, updated_at = now()`,
			p.Name, p.Type, p.Adapter, spec,
		).Error; err != nil {
			return err
		}
		return bumpGeneration(tx)
	})
	r.saveAfterMutation(ctx, err)
	return err
}

// ProviderPatch holds optional fields for a partial provider update. A nil
// field means "leave unchanged" (not "clear"). PatchProvider applies the patch
// in a transaction with SELECT ... FOR UPDATE so concurrent patches to the
// same provider serialize rather than lost-update. Returns (zero, false, nil)
// when the named provider does not exist; the caller maps that to a 404.
type ProviderPatch struct {
	Type      *string                  `json:"type,omitempty"`
	Adapter   *string                  `json:"adapter,omitempty"`
	BaseURL   *string                  `json:"base_url,omitempty"`
	APIKeyRef *string                  `json:"api_key_ref,omitempty"`
	Timeouts  *config.ProviderTimeouts `json:"timeouts,omitempty"`
	Weight    *int                     `json:"weight,omitempty"`
}

// PatchProvider applies a partial update to the named provider.
func (r *ConfigRepo) PatchProvider(ctx context.Context, name string, patch ProviderPatch) (config.Provider, bool, error) {
	var result config.Provider
	found := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var specBytes string
		res := tx.Raw(`SELECT spec::text FROM providers WHERE name = ? FOR UPDATE`, name).Scan(&specBytes)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // no row → not found
		}
		var p config.Provider
		if err := json.Unmarshal([]byte(specBytes), &p); err != nil {
			return err
		}
		// Apply patch: nil fields are left untouched.
		if patch.Type != nil {
			p.Type = *patch.Type
		}
		if patch.Adapter != nil {
			p.Adapter = *patch.Adapter
		}
		if patch.BaseURL != nil {
			p.BaseURL = *patch.BaseURL
		}
		if patch.APIKeyRef != nil {
			p.APIKeyRef = *patch.APIKeyRef
		}
		if patch.Timeouts != nil {
			p.Timeouts = *patch.Timeouts
		}
		if patch.Weight != nil {
			p.Weight = *patch.Weight
		}
		newSpec, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if err := tx.Exec(
			`UPDATE providers SET type = ?, adapter = ?, spec = ?, updated_at = now() WHERE name = ?`,
			p.Type, p.Adapter, newSpec, name,
		).Error; err != nil {
			return err
		}
		if err := bumpGeneration(tx); err != nil {
			return err
		}
		result = p
		found = true
		return nil
	})
	r.saveAfterMutation(ctx, err)
	return result, found, err
}

// ListProviders returns all stored providers.
func (r *ConfigRepo) ListProviders(ctx context.Context) ([]config.Provider, error) {
	return listSpecs[config.Provider](ctx, r.db, "providers")
}

// GetProvider returns the provider spec by name. ok is false when no provider
// with that name exists.
func (r *ConfigRepo) GetProvider(ctx context.Context, name string) (config.Provider, bool, error) {
	var spec string
	if err := r.db.WithContext(ctx).Raw(`SELECT spec::text FROM providers WHERE name = ?`, name).Scan(&spec).Error; err != nil {
		return config.Provider{}, false, err
	}
	if spec == "" {
		return config.Provider{}, false, nil
	}
	var p config.Provider
	if err := json.Unmarshal([]byte(spec), &p); err != nil {
		return config.Provider{}, false, err
	}
	return p, true, nil
}

// DeleteProvider removes a provider by name.
func (r *ConfigRepo) DeleteProvider(ctx context.Context, name string) error {
	err := r.deleteByCol(ctx, "providers", "name", name)
	r.saveAfterMutation(ctx, err)
	return err
}

// --- Models ---

// UpsertModel inserts or replaces a model by alias.
func (r *ConfigRepo) UpsertModel(ctx context.Context, m config.Model) error {
	spec, err := json.Marshal(m)
	if err != nil {
		return err
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`INSERT INTO models (alias, enabled, spec, updated_at)
			 VALUES (?, true, ?, now())
			 ON CONFLICT (alias) DO UPDATE SET spec = EXCLUDED.spec, updated_at = now()`,
			m.Alias, spec,
		).Error; err != nil {
			return err
		}
		return bumpGeneration(tx)
	})
	r.saveAfterMutation(ctx, err)
	return err
}

// ListModels returns all stored models.
func (r *ConfigRepo) ListModels(ctx context.Context) ([]config.Model, error) {
	return listSpecs[config.Model](ctx, r.db, "models")
}

// ModelPatch holds optional fields for a partial model update. nil means leave
// unchanged. PatchModel applies the patch in a SELECT ... FOR UPDATE tx.
type ModelPatch struct {
	Description   *string                 `json:"description,omitempty"`
	ContextLength *int                    `json:"context_length,omitempty"`
	Capabilities  *[]string               `json:"capabilities,omitempty"`
	Tags          *[]string               `json:"tags,omitempty"`
	Upstreams     *[]config.ModelUpstream `json:"upstreams,omitempty"`
}

// PatchModel applies a partial update to the named model (ADR-0030).
func (r *ConfigRepo) PatchModel(ctx context.Context, alias string, patch ModelPatch) (config.Model, bool, error) {
	var result config.Model
	found := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var specBytes string
		res := tx.Raw(`SELECT spec::text FROM models WHERE alias = ? FOR UPDATE`, alias).Scan(&specBytes)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		var m config.Model
		if err := json.Unmarshal([]byte(specBytes), &m); err != nil {
			return err
		}
		if patch.Description != nil {
			m.Description = *patch.Description
		}
		if patch.ContextLength != nil {
			m.ContextLength = *patch.ContextLength
		}
		if patch.Capabilities != nil {
			m.Capabilities = *patch.Capabilities
		}
		if patch.Tags != nil {
			m.Tags = *patch.Tags
		}
		if patch.Upstreams != nil {
			m.Upstreams = *patch.Upstreams
		}
		newSpec, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE models SET spec = ?, updated_at = now() WHERE alias = ?`, newSpec, alias).Error; err != nil {
			return err
		}
		if err := bumpGeneration(tx); err != nil {
			return err
		}
		result = m
		found = true
		return nil
	})
	r.saveAfterMutation(ctx, err)
	return result, found, err
}

// ListModelsPaged returns models with keyset pagination (by id).
func (r *ConfigRepo) ListModelsPaged(ctx context.Context, cursor string, limit int) ([]config.Model, string, error) {
	return listSpecsPaged[config.Model](ctx, r.db, "models", cursor, limit)
}

// GetModel returns the model spec by alias. ok is false when no model with that
// alias exists.
func (r *ConfigRepo) GetModel(ctx context.Context, alias string) (config.Model, bool, error) {
	var spec string
	if err := r.db.WithContext(ctx).Raw(`SELECT spec::text FROM models WHERE alias = ?`, alias).Scan(&spec).Error; err != nil {
		return config.Model{}, false, err
	}
	if spec == "" {
		return config.Model{}, false, nil
	}
	var m config.Model
	if err := json.Unmarshal([]byte(spec), &m); err != nil {
		return config.Model{}, false, err
	}
	return m, true, nil
}

// DeleteModel removes a model by alias.
func (r *ConfigRepo) DeleteModel(ctx context.Context, alias string) error {
	err := r.deleteByCol(ctx, "models", "alias", alias)
	r.saveAfterMutation(ctx, err)
	return err
}

// --- Routes ---

// UpsertRoute inserts or replaces a route by model alias.
func (r *ConfigRepo) UpsertRoute(ctx context.Context, rt config.Route) error {
	spec, err := json.Marshal(rt)
	if err != nil {
		return err
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`INSERT INTO routes (model_alias, strategy, enabled, spec, updated_at)
			 VALUES (?, ?, true, ?, now())
			 ON CONFLICT (model_alias) DO UPDATE SET strategy = EXCLUDED.strategy,
			     spec = EXCLUDED.spec, updated_at = now()`,
			rt.ModelAlias, rt.Strategy, spec,
		).Error; err != nil {
			return err
		}
		return bumpGeneration(tx)
	})
	r.saveAfterMutation(ctx, err)
	return err
}

// ListRoutes returns all stored routes.
func (r *ConfigRepo) ListRoutes(ctx context.Context) ([]config.Route, error) {
	return listSpecs[config.Route](ctx, r.db, "routes")
}

// RoutePatch holds optional fields for a partial route update.
type RoutePatch struct {
	Strategy  *string                 `json:"strategy,omitempty"`
	Providers *[]config.RouteProvider `json:"providers,omitempty"`
}

// PatchRoute applies a partial update to the named route, syncing the
// denormalized strategy column (ADR-0030).
func (r *ConfigRepo) PatchRoute(ctx context.Context, modelAlias string, patch RoutePatch) (config.Route, bool, error) {
	var result config.Route
	found := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var specBytes string
		res := tx.Raw(`SELECT spec::text FROM routes WHERE model_alias = ? FOR UPDATE`, modelAlias).Scan(&specBytes)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		var rt config.Route
		if err := json.Unmarshal([]byte(specBytes), &rt); err != nil {
			return err
		}
		if patch.Strategy != nil {
			rt.Strategy = *patch.Strategy
		}
		if patch.Providers != nil {
			rt.Providers = *patch.Providers
		}
		newSpec, err := json.Marshal(rt)
		if err != nil {
			return err
		}
		if err := tx.Exec(
			`UPDATE routes SET strategy = ?, spec = ?, updated_at = now() WHERE model_alias = ?`,
			rt.Strategy, newSpec, modelAlias,
		).Error; err != nil {
			return err
		}
		if err := bumpGeneration(tx); err != nil {
			return err
		}
		result = rt
		found = true
		return nil
	})
	r.saveAfterMutation(ctx, err)
	return result, found, err
}

// ListRoutesPaged returns routes with keyset pagination.
func (r *ConfigRepo) ListRoutesPaged(ctx context.Context, cursor string, limit int) ([]config.Route, string, error) {
	return listSpecsPaged[config.Route](ctx, r.db, "routes", cursor, limit)
}

// GetRoute returns the route spec by model_alias. ok is false when no route with
// that model_alias exists.
func (r *ConfigRepo) GetRoute(ctx context.Context, modelAlias string) (config.Route, bool, error) {
	var spec string
	if err := r.db.WithContext(ctx).Raw(`SELECT spec::text FROM routes WHERE model_alias = ?`, modelAlias).Scan(&spec).Error; err != nil {
		return config.Route{}, false, err
	}
	if spec == "" {
		return config.Route{}, false, nil
	}
	var rt config.Route
	if err := json.Unmarshal([]byte(spec), &rt); err != nil {
		return config.Route{}, false, err
	}
	return rt, true, nil
}

// DeleteRoute removes a route by model alias.
func (r *ConfigRepo) DeleteRoute(ctx context.Context, modelAlias string) error {
	err := r.deleteByCol(ctx, "routes", "model_alias", modelAlias)
	r.saveAfterMutation(ctx, err)
	return err
}

// --- Plugins ---

// UpsertPlugin inserts or replaces a plugin by (name, scope). Plugins are not
// uniquely keyed by a single identity column, so this deletes any existing
// (name, scope) and inserts — all within one version-bumping transaction.
func (r *ConfigRepo) UpsertPlugin(ctx context.Context, pc config.PluginConfig) error {
	spec, err := json.Marshal(pc)
	if err != nil {
		return err
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`DELETE FROM plugins WHERE name = ? AND scope = ?`, pc.Name, pc.Scope,
		).Error; err != nil {
			return err
		}
		if err := tx.Exec(
			`INSERT INTO plugins (name, phase, scope, enabled, spec, updated_at)
			 VALUES (?, ?, ?, ?, ?, now())`,
			pc.Name, pc.Phase, pc.Scope, pc.Enabled, spec,
		).Error; err != nil {
			return err
		}
		return bumpGeneration(tx)
	})
	r.saveAfterMutation(ctx, err)
	return err
}

// ListPlugins returns all stored plugin configs.
func (r *ConfigRepo) ListPlugins(ctx context.Context) ([]config.PluginConfig, error) {
	return listSpecs[config.PluginConfig](ctx, r.db, "plugins")
}

// PluginPatch holds optional fields for a partial plugin update. Scope is the
// identity composite key part and is NOT patchable here — it locates the row.
type PluginPatch struct {
	Phase   *string         `json:"phase,omitempty"`
	Params  *map[string]any `json:"params,omitempty"`
	Enabled *bool           `json:"enabled,omitempty"`
}

// PatchPlugin applies a partial update to the named plugin (composite key
// name+scope), syncing the denormalized phase + enabled columns. Unlike
// UpsertPlugin (DELETE+INSERT), this uses UPDATE to preserve the row id and
// hold a row lock via SELECT ... FOR UPDATE (ADR-0030).
func (r *ConfigRepo) PatchPlugin(ctx context.Context, name, scope string, patch PluginPatch) (config.PluginConfig, bool, error) {
	var result config.PluginConfig
	found := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var specBytes string
		res := tx.Raw(`SELECT spec::text FROM plugins WHERE name = ? AND scope = ? FOR UPDATE`, name, scope).Scan(&specBytes)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		var pc config.PluginConfig
		if err := json.Unmarshal([]byte(specBytes), &pc); err != nil {
			return err
		}
		if patch.Phase != nil {
			pc.Phase = *patch.Phase
		}
		if patch.Params != nil {
			pc.Params = *patch.Params
		}
		if patch.Enabled != nil {
			pc.Enabled = *patch.Enabled
		}
		newSpec, err := json.Marshal(pc)
		if err != nil {
			return err
		}
		if err := tx.Exec(
			`UPDATE plugins SET phase = ?, enabled = ?, spec = ?, updated_at = now() WHERE name = ? AND scope = ?`,
			pc.Phase, pc.Enabled, newSpec, name, scope,
		).Error; err != nil {
			return err
		}
		if err := bumpGeneration(tx); err != nil {
			return err
		}
		result = pc
		found = true
		return nil
	})
	r.saveAfterMutation(ctx, err)
	return result, found, err
}

// ListPluginsPaged returns plugins with keyset pagination.
func (r *ConfigRepo) ListPluginsPaged(ctx context.Context, cursor string, limit int) ([]config.PluginConfig, string, error) {
	return listSpecsPaged[config.PluginConfig](ctx, r.db, "plugins", cursor, limit)
}

// GetPlugin returns the plugin spec by (name, scope). ok is false when none
// matches. scope="" matches the global (scope=”) row.
func (r *ConfigRepo) GetPlugin(ctx context.Context, name, scope string) (config.PluginConfig, bool, error) {
	var spec string
	if err := r.db.WithContext(ctx).Raw(
		`SELECT spec::text FROM plugins WHERE name = ? AND scope = ?`, name, scope,
	).Scan(&spec).Error; err != nil {
		return config.PluginConfig{}, false, err
	}
	if spec == "" {
		return config.PluginConfig{}, false, nil
	}
	var pc config.PluginConfig
	if err := json.Unmarshal([]byte(spec), &pc); err != nil {
		return config.PluginConfig{}, false, err
	}
	return pc, true, nil
}

// DeletePlugin removes a plugin by (name, scope).
func (r *ConfigRepo) DeletePlugin(ctx context.Context, name, scope string) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM plugins WHERE name = ? AND scope = ?`, name, scope).Error; err != nil {
			return err
		}
		return bumpGeneration(tx)
	})
	r.saveAfterMutation(ctx, err)
	return err
}

// --- Snapshot ---

// Snapshot assembles the current dynamic config the data plane polls. Version is
// the config_generation counter; each section is unmarshaled from its spec JSONB
// (ADR-0014/0015).
func (r *ConfigRepo) Snapshot(ctx context.Context) (*config.Dynamic, error) {
	var version int64
	if err := r.db.WithContext(ctx).Raw(`SELECT version FROM config_generation`).Scan(&version).Error; err != nil {
		return nil, err
	}

	providers, err := r.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	models, err := r.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := r.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	plugins, err := r.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := r.GetSettings(ctx)
	if err != nil {
		return nil, err
	}

	return &config.Dynamic{
		Version:   strconv.FormatInt(version, 10),
		Providers: providers,
		Models:    models,
		Routes:    routes,
		Plugins:   plugins,
		Settings:  settings,
	}, nil
}

// deleteByCol removes rows matching col=val and bumps the generation.
func (r *ConfigRepo) deleteByCol(ctx context.Context, table, col, val string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM `+table+` WHERE `+col+` = ?`, val).Error; err != nil {
			return err
		}
		return bumpGeneration(tx)
	})
}

// --- Gateway settings ---

// GetSettings reads the single gateway_settings.spec document and unmarshals it
// into a GatewaySettings. Returns a zero-value GatewaySettings (not nil) when
// the row exists but spec is '{}' (the seeded default), so callers always get a
// usable pointer.
func (r *ConfigRepo) GetSettings(ctx context.Context) (*config.GatewaySettings, error) {
	var raw string
	if err := r.db.WithContext(ctx).
		Raw(`SELECT spec::text FROM gateway_settings WHERE id = 1`).Scan(&raw).Error; err != nil {
		return nil, err
	}
	s := &config.GatewaySettings{}
	if raw != "" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), s); err != nil {
			return nil, fmt.Errorf("store: parse gateway_settings.spec: %w", err)
		}
	}
	return s, nil
}

// UpdateSettings replaces the gateway_settings.spec document with s and bumps
// config_generation in the same transaction, so the snapshot version tracks the
// change and the data plane picks it up on the next poll.
func (r *ConfigRepo) UpdateSettings(ctx context.Context, s *config.GatewaySettings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("store: marshal gateway settings: %w", err)
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Upsert the single row (id=1 always exists from the migration, but
		// INSERT ... ON CONFLICT is robust against a truncated table).
		if err := tx.Exec(`
			INSERT INTO gateway_settings (id, spec, updated_at) VALUES (1, ?, now())
			ON CONFLICT (id) DO UPDATE SET spec = EXCLUDED.spec, updated_at = now()`,
			json.RawMessage(b)).Error; err != nil {
			return err
		}
		return bumpGeneration(tx)
	})
	r.saveAfterMutation(ctx, err) // save a snapshot for history/diff/rollback (ADR-0025)
	return err
}

// listSpecs reads the spec JSONB column from a config table and unmarshals each
// into T, preserving insertion order (by id). Used by Snapshot (no pagination).
func listSpecs[T any](ctx context.Context, db *DB, table string) ([]T, error) {
	var specs [][]byte
	if err := db.WithContext(ctx).Raw(`SELECT spec FROM ` + table + ` ORDER BY id`).Scan(&specs).Error; err != nil {
		return nil, err
	}
	out := make([]T, 0, len(specs))
	for _, raw := range specs {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// listSpecsPaged reads the spec JSONB column with optional keyset pagination.
// cursor is the opaque last-id from a prior page ("" = first page). limit<=0
// defaults to 50. Returns (rows, nextCursor); nextCursor is "" when there is
// no further page. cursor is base64-encoded last row id.
func listSpecsPaged[T any](ctx context.Context, db *DB, table string, cursor string, limit int) ([]T, string, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var lastID int64
	if cursor != "" {
		raw, err := base64.StdEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		if _, err := fmt.Sscanf(string(raw), "%d", &lastID); err != nil {
			return nil, "", fmt.Errorf("invalid cursor payload: %w", err)
		}
	}
	// Fetch limit+1 rows so we can tell whether there is a next page.
	query := `SELECT id, spec FROM ` + table + ` WHERE id > ? ORDER BY id LIMIT ?`
	if cursor == "" {
		query = `SELECT id, spec FROM ` + table + ` ORDER BY id LIMIT ?`
	}
	type row struct {
		ID   int64
		Spec []byte
	}
	var rows []row
	var err error
	if cursor == "" {
		err = db.WithContext(ctx).Raw(query, limit+1).Scan(&rows).Error
	} else {
		err = db.WithContext(ctx).Raw(query, lastID, limit+1).Scan(&rows).Error
	}
	if err != nil {
		return nil, "", err
	}
	hasNext := len(rows) > limit
	if hasNext {
		rows = rows[:limit]
	}
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		var v T
		if err := json.Unmarshal(r.Spec, &v); err != nil {
			return nil, "", err
		}
		out = append(out, v)
	}
	nextCursor := ""
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", last.ID)))
	}
	return out, nextCursor, nil
}

// ProviderReferencedBy returns the model aliases and route model_aliases that
// reference this provider in their JSONB spec. Used by the delete-guard so the
// handler can reject deletion with a clear 409. Both arrays are empty when
// there are no references.
func (r *ConfigRepo) ProviderReferencedBy(ctx context.Context, providerName string) (models []string, routes []string, err error) {
	// JSONB @> checks containment: spec->upstreams (array) contains an element
	// whose provider key matches. jsonb_build_array/jsonb_build_object produce
	// the comparison value with proper parameterization (no SQL injection).
	if err := r.db.WithContext(ctx).Raw(
		`SELECT alias FROM models
		 WHERE spec->'upstreams' @> jsonb_build_array(jsonb_build_object('provider', ?::text))
		 ORDER BY alias`, providerName,
	).Scan(&models).Error; err != nil {
		return nil, nil, err
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT model_alias FROM routes
		 WHERE spec->'providers' @> jsonb_build_array(jsonb_build_object('name', ?::text))
		 ORDER BY model_alias`, providerName,
	).Scan(&routes).Error; err != nil {
		return nil, nil, err
	}
	return models, routes, nil
}

// ModelReferencedBy returns the route model_aliases that reference this model
// (routes store model_alias directly, not in spec JSONB).
func (r *ConfigRepo) ModelReferencedBy(ctx context.Context, modelAlias string) (routes []string, err error) {
	if err := r.db.WithContext(ctx).Raw(
		`SELECT model_alias FROM routes WHERE model_alias = ? ORDER BY model_alias`, modelAlias,
	).Scan(&routes).Error; err != nil {
		return nil, err
	}
	return routes, nil
}
