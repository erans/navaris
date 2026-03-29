package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/navaris/navaris/internal/domain"
	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type projectStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

func (s *Store) ProjectStore() domain.ProjectStore {
	return &projectStore{readDB: s.readDB, writeDB: s.writeDB}
}

func (ps *projectStore) Create(ctx context.Context, p *domain.Project) error {
	meta, err := marshalJSON(p.Metadata)
	if err != nil {
		return err
	}
	_, err = ps.writeDB.ExecContext(ctx,
		`INSERT INTO projects (project_id, name, created_at, updated_at, metadata) VALUES (?, ?, ?, ?, ?)`,
		p.ProjectID, p.Name, p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano), meta)
	if err != nil {
		return mapError(err)
	}
	return nil
}

func (ps *projectStore) Get(ctx context.Context, id string) (*domain.Project, error) {
	row := ps.readDB.QueryRowContext(ctx,
		`SELECT project_id, name, created_at, updated_at, metadata FROM projects WHERE project_id = ?`, id)
	return scanProject(row)
}

func (ps *projectStore) GetByName(ctx context.Context, name string) (*domain.Project, error) {
	row := ps.readDB.QueryRowContext(ctx,
		`SELECT project_id, name, created_at, updated_at, metadata FROM projects WHERE name = ?`, name)
	return scanProject(row)
}

func (ps *projectStore) List(ctx context.Context) ([]*domain.Project, error) {
	rows, err := ps.readDB.QueryContext(ctx,
		`SELECT project_id, name, created_at, updated_at, metadata FROM projects ORDER BY name`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var projects []*domain.Project
	for rows.Next() {
		p, err := scanProjectRow(rows)
		if err != nil {
			return nil, mapError(err)
		}
		projects = append(projects, p)
	}
	return projects, mapError(rows.Err())
}

func (ps *projectStore) Update(ctx context.Context, p *domain.Project) error {
	meta, err := marshalJSON(p.Metadata)
	if err != nil {
		return err
	}
	res, err := ps.writeDB.ExecContext(ctx,
		`UPDATE projects SET name = ?, updated_at = ?, metadata = ? WHERE project_id = ?`,
		p.Name, p.UpdatedAt.Format(time.RFC3339Nano), meta, p.ProjectID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (ps *projectStore) Delete(ctx context.Context, id string) error {
	res, err := ps.writeDB.ExecContext(ctx, `DELETE FROM projects WHERE project_id = ?`, id)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func scanProject(row *sql.Row) (*domain.Project, error) {
	var p domain.Project
	var createdAt, updatedAt string
	var meta sql.NullString
	err := row.Scan(&p.ProjectID, &p.Name, &createdAt, &updatedAt, &meta)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project: %w", domain.ErrNotFound)
		}
		return nil, mapError(err)
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if meta.Valid {
		json.Unmarshal([]byte(meta.String), &p.Metadata)
	}
	return &p, nil
}

func scanProjectRow(rows *sql.Rows) (*domain.Project, error) {
	var p domain.Project
	var createdAt, updatedAt string
	var meta sql.NullString
	err := rows.Scan(&p.ProjectID, &p.Name, &createdAt, &updatedAt, &meta)
	if err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if meta.Valid {
		json.Unmarshal([]byte(meta.String), &p.Metadata)
	}
	return &p, nil
}

// Shared helpers

func marshalJSON(v any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		// Normalize to primary code to catch extended variants
		// (e.g. SQLITE_LOCKED_SHAREDCACHE).
		switch sqliteErr.Code() & 0xFF {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return fmt.Errorf("%w: %s", domain.ErrBusy, sqliteErr.Error())
		}
	}
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint failed") {
		return fmt.Errorf("%w: %s", domain.ErrConflict, msg)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w", domain.ErrNotFound)
	}
	return err
}

func checkRowsAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w", domain.ErrNotFound)
	}
	return nil
}
