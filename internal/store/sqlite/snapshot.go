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

type snapshotStore struct {
	db *sql.DB
}

func (s *Store) SnapshotStore() domain.SnapshotStore {
	return &snapshotStore{db: s.db}
}

func (ss *snapshotStore) Create(ctx context.Context, snap *domain.Snapshot) error {
	meta, err := marshalJSON(snap.Metadata)
	if err != nil {
		return err
	}
	publishable := 0
	if snap.Publishable {
		publishable = 1
	}
	_, err = ss.db.ExecContext(ctx, `INSERT INTO snapshots
		(snapshot_id, sandbox_id, backend, backend_ref, label, state,
		 created_at, updated_at, parent_image_id, publishable, consistency_mode, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.SnapshotID, snap.SandboxID, snap.Backend, nullString(snap.BackendRef),
		snap.Label, string(snap.State),
		snap.CreatedAt.Format(time.RFC3339Nano), snap.UpdatedAt.Format(time.RFC3339Nano),
		nullString(snap.ParentImageID), publishable, string(snap.ConsistencyMode), meta)
	return mapError(err)
}

func (ss *snapshotStore) Get(ctx context.Context, id string) (*domain.Snapshot, error) {
	row := ss.db.QueryRowContext(ctx, `SELECT
		snapshot_id, sandbox_id, backend, backend_ref, label, state,
		created_at, updated_at, parent_image_id, publishable, consistency_mode, metadata
		FROM snapshots WHERE snapshot_id = ?`, id)
	return scanSnapshot(row)
}

func (ss *snapshotStore) ListBySandbox(ctx context.Context, sandboxID string) ([]*domain.Snapshot, error) {
	rows, err := ss.db.QueryContext(ctx, `SELECT
		snapshot_id, sandbox_id, backend, backend_ref, label, state,
		created_at, updated_at, parent_image_id, publishable, consistency_mode, metadata
		FROM snapshots WHERE sandbox_id = ? ORDER BY created_at`, sandboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snaps []*domain.Snapshot
	for rows.Next() {
		snap, err := scanSnapshotRow(rows)
		if err != nil {
			return nil, err
		}
		snaps = append(snaps, snap)
	}
	return snaps, rows.Err()
}

func (ss *snapshotStore) Update(ctx context.Context, snap *domain.Snapshot) error {
	meta, err := marshalJSON(snap.Metadata)
	if err != nil {
		return err
	}
	publishable := 0
	if snap.Publishable {
		publishable = 1
	}
	res, err := ss.db.ExecContext(ctx, `UPDATE snapshots SET
		backend_ref = ?, label = ?, state = ?, updated_at = ?,
		parent_image_id = ?, publishable = ?, consistency_mode = ?, metadata = ?
		WHERE snapshot_id = ?`,
		nullString(snap.BackendRef), snap.Label, string(snap.State),
		snap.UpdatedAt.Format(time.RFC3339Nano),
		nullString(snap.ParentImageID), publishable, string(snap.ConsistencyMode),
		meta, snap.SnapshotID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (ss *snapshotStore) Delete(ctx context.Context, id string) error {
	res, err := ss.db.ExecContext(ctx, `DELETE FROM snapshots WHERE snapshot_id = ?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

func (ss *snapshotStore) ListOrphaned(ctx context.Context) ([]*domain.Snapshot, error) {
	rows, err := ss.db.QueryContext(ctx, `SELECT s.snapshot_id, s.sandbox_id, s.backend, s.backend_ref,
		s.label, s.state, s.created_at, s.updated_at, s.parent_image_id,
		s.publishable, s.consistency_mode, s.metadata
		FROM snapshots s
		JOIN sandboxes sb ON s.sandbox_id = sb.sandbox_id
		WHERE sb.state = 'destroyed'
		AND s.snapshot_id NOT IN (
			SELECT source_snapshot_id FROM base_images WHERE source_snapshot_id IS NOT NULL
		)
		AND s.state != 'deleted'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snaps []*domain.Snapshot
	for rows.Next() {
		snap, err := scanSnapshotRow(rows)
		if err != nil {
			return nil, err
		}
		snaps = append(snaps, snap)
	}
	return snaps, rows.Err()
}

func scanSnapshot(row *sql.Row) (*domain.Snapshot, error) {
	var snap domain.Snapshot
	var state, consistencyMode, createdAt, updatedAt string
	var backendRef, parentImageID, meta sql.NullString
	var publishable int

	err := row.Scan(&snap.SnapshotID, &snap.SandboxID, &snap.Backend, &backendRef,
		&snap.Label, &state, &createdAt, &updatedAt, &parentImageID,
		&publishable, &consistencyMode, &meta)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("snapshot: %w", domain.ErrNotFound)
		}
		return nil, err
	}
	populateSnapshot(&snap, state, consistencyMode, createdAt, updatedAt, backendRef, parentImageID, publishable, meta)
	return &snap, nil
}

func scanSnapshotRow(rows *sql.Rows) (*domain.Snapshot, error) {
	var snap domain.Snapshot
	var state, consistencyMode, createdAt, updatedAt string
	var backendRef, parentImageID, meta sql.NullString
	var publishable int

	err := rows.Scan(&snap.SnapshotID, &snap.SandboxID, &snap.Backend, &backendRef,
		&snap.Label, &state, &createdAt, &updatedAt, &parentImageID,
		&publishable, &consistencyMode, &meta)
	if err != nil {
		return nil, err
	}
	populateSnapshot(&snap, state, consistencyMode, createdAt, updatedAt, backendRef, parentImageID, publishable, meta)
	return &snap, nil
}

func populateSnapshot(snap *domain.Snapshot, state, consistencyMode, createdAt, updatedAt string,
	backendRef, parentImageID sql.NullString, publishable int, meta sql.NullString) {
	snap.State = domain.SnapshotState(state)
	snap.ConsistencyMode = domain.ConsistencyMode(consistencyMode)
	snap.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	snap.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	snap.Publishable = publishable != 0
	if backendRef.Valid {
		snap.BackendRef = backendRef.String
	}
	if parentImageID.Valid {
		snap.ParentImageID = parentImageID.String
	}
	if meta.Valid {
		json.Unmarshal([]byte(meta.String), &snap.Metadata)
	}
}
