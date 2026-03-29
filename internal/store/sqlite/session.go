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

type sessionStore struct {
	db *sql.DB
}

func (s *Store) SessionStore() domain.SessionStore {
	return &sessionStore{db: s.db}
}

func (ss *sessionStore) Create(ctx context.Context, sess *domain.Session) error {
	meta, err := marshalJSON(sess.Metadata)
	if err != nil {
		return err
	}
	var idleTimeoutSec sql.NullInt64
	if sess.IdleTimeout != nil {
		idleTimeoutSec = sql.NullInt64{Int64: int64(sess.IdleTimeout.Seconds()), Valid: true}
	}
	_, err = ss.db.ExecContext(ctx, `INSERT INTO sessions
		(session_id, sandbox_id, backing, shell, state,
		 created_at, updated_at, last_attached_at, idle_timeout_sec, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.SessionID, sess.SandboxID, string(sess.Backing), sess.Shell,
		string(sess.State), sess.CreatedAt.Format(time.RFC3339Nano),
		sess.UpdatedAt.Format(time.RFC3339Nano), nullTime(sess.LastAttachedAt),
		idleTimeoutSec, meta)
	return mapError(err)
}

func (ss *sessionStore) Get(ctx context.Context, id string) (*domain.Session, error) {
	row := ss.db.QueryRowContext(ctx, `SELECT
		session_id, sandbox_id, backing, shell, state,
		created_at, updated_at, last_attached_at, idle_timeout_sec, metadata
		FROM sessions WHERE session_id = ?`, id)
	return scanSession(row)
}

func (ss *sessionStore) ListBySandbox(ctx context.Context, sandboxID string) ([]*domain.Session, error) {
	rows, err := ss.db.QueryContext(ctx, `SELECT
		session_id, sandbox_id, backing, shell, state,
		created_at, updated_at, last_attached_at, idle_timeout_sec, metadata
		FROM sessions WHERE sandbox_id = ? ORDER BY created_at`, sandboxID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var sessions []*domain.Session
	for rows.Next() {
		sess, err := scanSessionRow(rows)
		if err != nil {
			return nil, mapError(err)
		}
		sessions = append(sessions, sess)
	}
	return sessions, mapError(rows.Err())
}

func (ss *sessionStore) Update(ctx context.Context, sess *domain.Session) error {
	meta, err := marshalJSON(sess.Metadata)
	if err != nil {
		return err
	}
	var idleTimeoutSec sql.NullInt64
	if sess.IdleTimeout != nil {
		idleTimeoutSec = sql.NullInt64{Int64: int64(sess.IdleTimeout.Seconds()), Valid: true}
	}
	res, err := ss.db.ExecContext(ctx, `UPDATE sessions SET
		backing = ?, shell = ?, state = ?, updated_at = ?,
		last_attached_at = ?, idle_timeout_sec = ?, metadata = ?
		WHERE session_id = ?`,
		string(sess.Backing), sess.Shell, string(sess.State),
		sess.UpdatedAt.Format(time.RFC3339Nano), nullTime(sess.LastAttachedAt),
		idleTimeoutSec, meta, sess.SessionID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (ss *sessionStore) Delete(ctx context.Context, id string) error {
	res, err := ss.db.ExecContext(ctx, `DELETE FROM sessions WHERE session_id = ?`, id)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func scanSession(row *sql.Row) (*domain.Session, error) {
	var sess domain.Session
	var backing, state, createdAt, updatedAt string
	var lastAttachedAt, meta sql.NullString
	var idleTimeoutSec sql.NullInt64

	err := row.Scan(&sess.SessionID, &sess.SandboxID, &backing, &sess.Shell, &state,
		&createdAt, &updatedAt, &lastAttachedAt, &idleTimeoutSec, &meta)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("session: %w", domain.ErrNotFound)
		}
		return nil, mapError(err)
	}
	populateSession(&sess, backing, state, createdAt, updatedAt, lastAttachedAt, idleTimeoutSec, meta)
	return &sess, nil
}

func scanSessionRow(rows *sql.Rows) (*domain.Session, error) {
	var sess domain.Session
	var backing, state, createdAt, updatedAt string
	var lastAttachedAt, meta sql.NullString
	var idleTimeoutSec sql.NullInt64

	err := rows.Scan(&sess.SessionID, &sess.SandboxID, &backing, &sess.Shell, &state,
		&createdAt, &updatedAt, &lastAttachedAt, &idleTimeoutSec, &meta)
	if err != nil {
		return nil, err
	}
	populateSession(&sess, backing, state, createdAt, updatedAt, lastAttachedAt, idleTimeoutSec, meta)
	return &sess, nil
}

func populateSession(sess *domain.Session, backing, state, createdAt, updatedAt string,
	lastAttachedAt sql.NullString, idleTimeoutSec sql.NullInt64, meta sql.NullString) {
	sess.Backing = domain.SessionBacking(backing)
	sess.State = domain.SessionState(state)
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if lastAttachedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, lastAttachedAt.String)
		sess.LastAttachedAt = &t
	}
	if idleTimeoutSec.Valid {
		d := time.Duration(idleTimeoutSec.Int64) * time.Second
		sess.IdleTimeout = &d
	}
	if meta.Valid {
		json.Unmarshal([]byte(meta.String), &sess.Metadata)
	}
}
