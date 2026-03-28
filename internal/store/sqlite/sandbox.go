package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

type sandboxStore struct {
	db *sql.DB
}

func (s *Store) SandboxStore() domain.SandboxStore {
	return &sandboxStore{db: s.db}
}

func (ss *sandboxStore) Create(ctx context.Context, sbx *domain.Sandbox) error {
	meta, err := marshalJSON(sbx.Metadata)
	if err != nil {
		return err
	}
	_, err = ss.db.ExecContext(ctx, `INSERT INTO sandboxes
		(sandbox_id, project_id, name, state, backend, backend_ref, host_id,
		 source_image_id, parent_snapshot_id, created_at, updated_at, expires_at,
		 cpu_limit, memory_limit_mb, network_mode, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sbx.SandboxID, sbx.ProjectID, sbx.Name, string(sbx.State), sbx.Backend,
		nullString(sbx.BackendRef), nullString(sbx.HostID),
		nullString(sbx.SourceImageID), nullString(sbx.ParentSnapshotID),
		sbx.CreatedAt.Format(time.RFC3339Nano), sbx.UpdatedAt.Format(time.RFC3339Nano),
		nullTime(sbx.ExpiresAt), nullInt(sbx.CPULimit), nullInt(sbx.MemoryLimitMB),
		string(sbx.NetworkMode), meta)
	return mapError(err)
}

func (ss *sandboxStore) Get(ctx context.Context, id string) (*domain.Sandbox, error) {
	row := ss.db.QueryRowContext(ctx, `SELECT
		sandbox_id, project_id, name, state, backend, backend_ref, host_id,
		source_image_id, parent_snapshot_id, created_at, updated_at, expires_at,
		cpu_limit, memory_limit_mb, network_mode, metadata
		FROM sandboxes WHERE sandbox_id = ?`, id)
	return scanSandbox(row)
}

func (ss *sandboxStore) List(ctx context.Context, f domain.SandboxFilter) ([]*domain.Sandbox, error) {
	query := `SELECT sandbox_id, project_id, name, state, backend, backend_ref, host_id,
		source_image_id, parent_snapshot_id, created_at, updated_at, expires_at,
		cpu_limit, memory_limit_mb, network_mode, metadata FROM sandboxes WHERE 1=1`
	var args []any
	if f.ProjectID != nil {
		query += " AND project_id = ?"
		args = append(args, *f.ProjectID)
	}
	if f.State != nil {
		query += " AND state = ?"
		args = append(args, string(*f.State))
	}
	if f.Backend != nil {
		query += " AND backend = ?"
		args = append(args, *f.Backend)
	}
	query += " ORDER BY created_at"
	rows, err := ss.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sandboxes []*domain.Sandbox
	for rows.Next() {
		sbx, err := scanSandboxRow(rows)
		if err != nil {
			return nil, err
		}
		sandboxes = append(sandboxes, sbx)
	}
	return sandboxes, rows.Err()
}

func (ss *sandboxStore) Update(ctx context.Context, sbx *domain.Sandbox) error {
	meta, err := marshalJSON(sbx.Metadata)
	if err != nil {
		return err
	}
	res, err := ss.db.ExecContext(ctx, `UPDATE sandboxes SET
		name = ?, state = ?, backend_ref = ?, host_id = ?,
		source_image_id = ?, parent_snapshot_id = ?,
		updated_at = ?, expires_at = ?, cpu_limit = ?, memory_limit_mb = ?,
		network_mode = ?, metadata = ?
		WHERE sandbox_id = ?`,
		sbx.Name, string(sbx.State), nullString(sbx.BackendRef), nullString(sbx.HostID),
		nullString(sbx.SourceImageID), nullString(sbx.ParentSnapshotID),
		sbx.UpdatedAt.Format(time.RFC3339Nano), nullTime(sbx.ExpiresAt),
		nullInt(sbx.CPULimit), nullInt(sbx.MemoryLimitMB),
		string(sbx.NetworkMode), meta, sbx.SandboxID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (ss *sandboxStore) Delete(ctx context.Context, id string) error {
	res, err := ss.db.ExecContext(ctx, `DELETE FROM sandboxes WHERE sandbox_id = ?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

func (ss *sandboxStore) ListExpired(ctx context.Context, now time.Time) ([]*domain.Sandbox, error) {
	rows, err := ss.db.QueryContext(ctx, `SELECT sandbox_id, project_id, name, state, backend, backend_ref, host_id,
		source_image_id, parent_snapshot_id, created_at, updated_at, expires_at,
		cpu_limit, memory_limit_mb, network_mode, metadata
		FROM sandboxes WHERE expires_at IS NOT NULL AND expires_at <= ? AND state != ?`,
		now.Format(time.RFC3339Nano), string(domain.SandboxDestroyed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sandboxes []*domain.Sandbox
	for rows.Next() {
		sbx, err := scanSandboxRow(rows)
		if err != nil {
			return nil, err
		}
		sandboxes = append(sandboxes, sbx)
	}
	return sandboxes, rows.Err()
}

func scanSandbox(row *sql.Row) (*domain.Sandbox, error) {
	var sbx domain.Sandbox
	var state, networkMode, createdAt, updatedAt string
	var backendRef, hostID, sourceImageID, parentSnapshotID, expiresAt, meta sql.NullString
	var cpuLimit, memoryLimit sql.NullInt64

	err := row.Scan(&sbx.SandboxID, &sbx.ProjectID, &sbx.Name, &state, &sbx.Backend,
		&backendRef, &hostID, &sourceImageID, &parentSnapshotID,
		&createdAt, &updatedAt, &expiresAt, &cpuLimit, &memoryLimit, &networkMode, &meta)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("sandbox: %w", domain.ErrNotFound)
		}
		return nil, err
	}
	return populateSandbox(&sbx, state, networkMode, createdAt, updatedAt, backendRef, hostID,
		sourceImageID, parentSnapshotID, expiresAt, cpuLimit, memoryLimit, meta), nil
}

func scanSandboxRow(rows *sql.Rows) (*domain.Sandbox, error) {
	var sbx domain.Sandbox
	var state, networkMode, createdAt, updatedAt string
	var backendRef, hostID, sourceImageID, parentSnapshotID, expiresAt, meta sql.NullString
	var cpuLimit, memoryLimit sql.NullInt64

	err := rows.Scan(&sbx.SandboxID, &sbx.ProjectID, &sbx.Name, &state, &sbx.Backend,
		&backendRef, &hostID, &sourceImageID, &parentSnapshotID,
		&createdAt, &updatedAt, &expiresAt, &cpuLimit, &memoryLimit, &networkMode, &meta)
	if err != nil {
		return nil, err
	}
	return populateSandbox(&sbx, state, networkMode, createdAt, updatedAt, backendRef, hostID,
		sourceImageID, parentSnapshotID, expiresAt, cpuLimit, memoryLimit, meta), nil
}

func populateSandbox(sbx *domain.Sandbox, state, networkMode, createdAt, updatedAt string,
	backendRef, hostID, sourceImageID, parentSnapshotID, expiresAt sql.NullString,
	cpuLimit, memoryLimit sql.NullInt64, meta sql.NullString) *domain.Sandbox {
	sbx.State = domain.SandboxState(state)
	sbx.NetworkMode = domain.NetworkMode(networkMode)
	sbx.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	sbx.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if backendRef.Valid {
		sbx.BackendRef = backendRef.String
	}
	if hostID.Valid {
		sbx.HostID = hostID.String
	}
	if sourceImageID.Valid {
		sbx.SourceImageID = sourceImageID.String
	}
	if parentSnapshotID.Valid {
		sbx.ParentSnapshotID = parentSnapshotID.String
	}
	if expiresAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, expiresAt.String)
		sbx.ExpiresAt = &t
	}
	if cpuLimit.Valid {
		v := int(cpuLimit.Int64)
		sbx.CPULimit = &v
	}
	if memoryLimit.Valid {
		v := int(memoryLimit.Int64)
		sbx.MemoryLimitMB = &v
	}
	if meta.Valid {
		json.Unmarshal([]byte(meta.String), &sbx.Metadata)
	}
	return sbx
}

// Nullable type helpers

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format(time.RFC3339Nano), Valid: true}
}

func nullInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}
