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

type operationStore struct {
	db *sql.DB
}

func (s *Store) OperationStore() domain.OperationStore {
	return &operationStore{db: s.db}
}

func (os *operationStore) Create(ctx context.Context, op *domain.Operation) error {
	meta, err := marshalJSON(op.Metadata)
	if err != nil {
		return err
	}
	var finishedAt sql.NullString
	if op.FinishedAt != nil {
		finishedAt = sql.NullString{String: op.FinishedAt.Format(time.RFC3339Nano), Valid: true}
	}
	_, err = os.db.ExecContext(ctx, `INSERT INTO operations
		(operation_id, resource_type, resource_id, sandbox_id, snapshot_id,
		 type, state, started_at, finished_at, error_text, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.OperationID, op.ResourceType, op.ResourceID,
		nullString(op.SandboxID), nullString(op.SnapshotID),
		op.Type, string(op.State), op.StartedAt.Format(time.RFC3339Nano),
		finishedAt, nullString(op.ErrorText), meta)
	return mapError(err)
}

func (os *operationStore) Get(ctx context.Context, id string) (*domain.Operation, error) {
	row := os.db.QueryRowContext(ctx, `SELECT
		operation_id, resource_type, resource_id, sandbox_id, snapshot_id,
		type, state, started_at, finished_at, error_text, metadata
		FROM operations WHERE operation_id = ?`, id)
	return scanOperation(row)
}

func (os *operationStore) List(ctx context.Context, f domain.OperationFilter) ([]*domain.Operation, error) {
	query := `SELECT operation_id, resource_type, resource_id, sandbox_id, snapshot_id,
		type, state, started_at, finished_at, error_text, metadata
		FROM operations WHERE 1=1`
	var args []any
	if f.ResourceType != nil {
		query += " AND resource_type = ?"
		args = append(args, *f.ResourceType)
	}
	if f.ResourceID != nil {
		query += " AND resource_id = ?"
		args = append(args, *f.ResourceID)
	}
	if f.SandboxID != nil {
		query += " AND sandbox_id = ?"
		args = append(args, *f.SandboxID)
	}
	if f.State != nil {
		query += " AND state = ?"
		args = append(args, string(*f.State))
	}
	query += " ORDER BY started_at DESC"
	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := os.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []*domain.Operation
	for rows.Next() {
		op, err := scanOperationRow(rows)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

func (os *operationStore) Update(ctx context.Context, op *domain.Operation) error {
	meta, err := marshalJSON(op.Metadata)
	if err != nil {
		return err
	}
	var finishedAt sql.NullString
	if op.FinishedAt != nil {
		finishedAt = sql.NullString{String: op.FinishedAt.Format(time.RFC3339Nano), Valid: true}
	}
	res, err := os.db.ExecContext(ctx, `UPDATE operations SET
		state = ?, finished_at = ?, error_text = ?, metadata = ?
		WHERE operation_id = ?`,
		string(op.State), finishedAt, nullString(op.ErrorText), meta, op.OperationID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (os *operationStore) ListStale(ctx context.Context, olderThan time.Time) ([]*domain.Operation, error) {
	rows, err := os.db.QueryContext(ctx, `SELECT
		operation_id, resource_type, resource_id, sandbox_id, snapshot_id,
		type, state, started_at, finished_at, error_text, metadata
		FROM operations
		WHERE state IN ('succeeded', 'failed', 'cancelled')
		AND finished_at IS NOT NULL AND finished_at <= ?
		ORDER BY finished_at`, olderThan.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []*domain.Operation
	for rows.Next() {
		op, err := scanOperationRow(rows)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

func (os *operationStore) ListByState(ctx context.Context, state domain.OperationState) ([]*domain.Operation, error) {
	rows, err := os.db.QueryContext(ctx, `SELECT
		operation_id, resource_type, resource_id, sandbox_id, snapshot_id,
		type, state, started_at, finished_at, error_text, metadata
		FROM operations WHERE state = ? ORDER BY started_at`, string(state))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []*domain.Operation
	for rows.Next() {
		op, err := scanOperationRow(rows)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

func scanOperation(row *sql.Row) (*domain.Operation, error) {
	var op domain.Operation
	var state, startedAt string
	var sandboxID, snapshotID, finishedAt, errorText, meta sql.NullString

	err := row.Scan(&op.OperationID, &op.ResourceType, &op.ResourceID,
		&sandboxID, &snapshotID, &op.Type, &state, &startedAt,
		&finishedAt, &errorText, &meta)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("operation: %w", domain.ErrNotFound)
		}
		return nil, err
	}
	populateOperation(&op, state, startedAt, sandboxID, snapshotID, finishedAt, errorText, meta)
	return &op, nil
}

func scanOperationRow(rows *sql.Rows) (*domain.Operation, error) {
	var op domain.Operation
	var state, startedAt string
	var sandboxID, snapshotID, finishedAt, errorText, meta sql.NullString

	err := rows.Scan(&op.OperationID, &op.ResourceType, &op.ResourceID,
		&sandboxID, &snapshotID, &op.Type, &state, &startedAt,
		&finishedAt, &errorText, &meta)
	if err != nil {
		return nil, err
	}
	populateOperation(&op, state, startedAt, sandboxID, snapshotID, finishedAt, errorText, meta)
	return &op, nil
}

func populateOperation(op *domain.Operation, state, startedAt string,
	sandboxID, snapshotID, finishedAt, errorText, meta sql.NullString) {
	op.State = domain.OperationState(state)
	op.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
	if sandboxID.Valid {
		op.SandboxID = sandboxID.String
	}
	if snapshotID.Valid {
		op.SnapshotID = snapshotID.String
	}
	if finishedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, finishedAt.String)
		op.FinishedAt = &t
	}
	if errorText.Valid {
		op.ErrorText = errorText.String
	}
	if meta.Valid {
		json.Unmarshal([]byte(meta.String), &op.Metadata)
	}
}
