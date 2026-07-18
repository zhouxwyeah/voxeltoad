package store

import (
	"context"
	"encoding/json"
	"time"
)

// DataPlaneNode is a row in data_plane_nodes, used by the admin UI to show
// cluster topology and instance health.
type DataPlaneNode struct {
	ID               int64     `json:"id"`
	InstanceID       string    `json:"instance_id"`
	Hostname         string    `json:"hostname"`
	Addr             string    `json:"addr"`
	Version          string    `json:"version"`
	Commit           string    `json:"commit"`
	ConfigGeneration int64     `json:"config_generation"`
	Status           string    `json:"status"`
	StartedAt        time.Time `json:"started_at"`
	LastHeartbeatAt  time.Time `json:"last_heartbeat_at"`
}

// DataPlaneRepo manages the data_plane_nodes table. The data plane (proxy)
// calls Register/Heartbeat/Drain; the admin plane lists nodes for the UI.
type DataPlaneRepo struct {
	db *DB
}

// NewDataPlaneRepo builds a DataPlaneRepo over the given connection.
func NewDataPlaneRepo(db *DB) *DataPlaneRepo {
	return &DataPlaneRepo{db: db}
}

// Register inserts or re-activates an instance. Idempotent by instance_id.
func (r *DataPlaneRepo) Register(ctx context.Context, n *DataPlaneNode) error {
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO data_plane_nodes (instance_id, hostname, addr, version, commit, config_generation, status, started_at, last_heartbeat_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'online', now(), now())
		 ON CONFLICT (instance_id) DO UPDATE SET
		     hostname = EXCLUDED.hostname,
		     addr = EXCLUDED.addr,
		     version = EXCLUDED.version,
		     commit = EXCLUDED.commit,
		     config_generation = EXCLUDED.config_generation,
		     status = 'online',
		     last_heartbeat_at = now()`,
		n.InstanceID, n.Hostname, n.Addr, n.Version, n.Commit, n.ConfigGeneration,
	).Error
}

// Heartbeat updates last_heartbeat_at for an instance. Call periodically.
func (r *DataPlaneRepo) Heartbeat(ctx context.Context, instanceID string) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE data_plane_nodes SET last_heartbeat_at = now() WHERE instance_id = ? AND status = 'online'`,
		instanceID,
	).Error
}

// Drain marks an instance as draining (shutting down gracefully). The status
// transitions through 'draining' to signal to the admin UI before the process
// exits; a dead instance is detected by stale heartbeat, not by this transition.
func (r *DataPlaneRepo) Drain(ctx context.Context, instanceID string) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE data_plane_nodes SET status = 'draining' WHERE instance_id = ?`,
		instanceID,
	).Error
}

// MarkOffline sets an instance status to offline. Called by the stale-node
// cleanup job, or when an instance finishes draining and exits.
func (r *DataPlaneRepo) MarkOffline(ctx context.Context, instanceID string) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE data_plane_nodes SET status = 'offline' WHERE instance_id = ?`,
		instanceID,
	).Error
}

// CleanupStale marks online nodes whose last heartbeat is older than maxAge as
// offline. Returns the number of nodes affected.
func (r *DataPlaneRepo) CleanupStale(ctx context.Context, maxAge time.Duration) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		`UPDATE data_plane_nodes SET status = 'offline'
		 WHERE status = 'online'
		   AND last_heartbeat_at < now() - make_interval(secs => ?)`,
		int(maxAge.Seconds()),
	)
	return res.RowsAffected, res.Error
}

// List returns all data-plane node records ordered by started_at.
func (r *DataPlaneRepo) List(ctx context.Context) ([]DataPlaneNode, error) {
	var nodes []DataPlaneNode
	if err := r.db.WithContext(ctx).Raw(
		`SELECT id, instance_id, hostname, addr, version, commit, config_generation, status, started_at, last_heartbeat_at
		 FROM data_plane_nodes ORDER BY started_at`,
	).Scan(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// OnlineCount returns the number of data-plane instances with status='online'.
func (r *DataPlaneRepo) OnlineCount(ctx context.Context) (int, error) {
	var n int
	if err := r.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM data_plane_nodes WHERE status = 'online'`,
	).Scan(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// UpdateBreakerStates persists per-instance circuit breaker state (provider →
// status). Used by the data-plane heartbeat goroutine for B2' visibility.
func (r *DataPlaneRepo) UpdateBreakerStates(ctx context.Context, instanceID string, states map[string]string) error {
	payload, err := json.Marshal(states)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(
		`UPDATE data_plane_nodes SET breaker_states = ? WHERE instance_id = ?`,
		payload, instanceID,
	).Error
}
