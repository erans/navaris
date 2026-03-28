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

type imageStore struct {
	db *sql.DB
}

func (s *Store) ImageStore() domain.ImageStore {
	return &imageStore{db: s.db}
}

func (is *imageStore) Create(ctx context.Context, img *domain.BaseImage) error {
	meta, err := marshalJSON(img.Metadata)
	if err != nil {
		return err
	}
	_, err = is.db.ExecContext(ctx, `INSERT INTO base_images
		(image_id, project_scope, name, version, source_type, source_snapshot_id,
		 backend, backend_ref, architecture, state, created_at, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		img.ImageID, nullString(img.ProjectScope), img.Name, img.Version,
		string(img.SourceType), nullString(img.SourceSnapshotID),
		img.Backend, nullString(img.BackendRef), img.Architecture,
		string(img.State), img.CreatedAt.Format(time.RFC3339Nano), meta)
	return mapError(err)
}

func (is *imageStore) Get(ctx context.Context, id string) (*domain.BaseImage, error) {
	row := is.db.QueryRowContext(ctx, `SELECT
		image_id, project_scope, name, version, source_type, source_snapshot_id,
		backend, backend_ref, architecture, state, created_at, metadata
		FROM base_images WHERE image_id = ?`, id)
	return scanImage(row)
}

func (is *imageStore) List(ctx context.Context, f domain.ImageFilter) ([]*domain.BaseImage, error) {
	query := `SELECT image_id, project_scope, name, version, source_type, source_snapshot_id,
		backend, backend_ref, architecture, state, created_at, metadata
		FROM base_images WHERE 1=1`
	var args []any
	if f.Name != nil {
		query += " AND name = ?"
		args = append(args, *f.Name)
	}
	if f.Architecture != nil {
		query += " AND architecture = ?"
		args = append(args, *f.Architecture)
	}
	if f.State != nil {
		query += " AND state = ?"
		args = append(args, string(*f.State))
	}
	query += " ORDER BY name, version"
	rows, err := is.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var images []*domain.BaseImage
	for rows.Next() {
		img, err := scanImageRow(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

func (is *imageStore) Update(ctx context.Context, img *domain.BaseImage) error {
	meta, err := marshalJSON(img.Metadata)
	if err != nil {
		return err
	}
	res, err := is.db.ExecContext(ctx, `UPDATE base_images SET
		project_scope = ?, name = ?, version = ?, source_type = ?, source_snapshot_id = ?,
		backend_ref = ?, architecture = ?, state = ?, metadata = ?
		WHERE image_id = ?`,
		nullString(img.ProjectScope), img.Name, img.Version,
		string(img.SourceType), nullString(img.SourceSnapshotID),
		nullString(img.BackendRef), img.Architecture, string(img.State),
		meta, img.ImageID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (is *imageStore) Delete(ctx context.Context, id string) error {
	res, err := is.db.ExecContext(ctx, `DELETE FROM base_images WHERE image_id = ?`, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

func scanImage(row *sql.Row) (*domain.BaseImage, error) {
	var img domain.BaseImage
	var sourceType, state, createdAt string
	var projectScope, sourceSnapshotID, backendRef, meta sql.NullString

	err := row.Scan(&img.ImageID, &projectScope, &img.Name, &img.Version,
		&sourceType, &sourceSnapshotID, &img.Backend, &backendRef,
		&img.Architecture, &state, &createdAt, &meta)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("image: %w", domain.ErrNotFound)
		}
		return nil, err
	}
	populateImage(&img, sourceType, state, createdAt, projectScope, sourceSnapshotID, backendRef, meta)
	return &img, nil
}

func scanImageRow(rows *sql.Rows) (*domain.BaseImage, error) {
	var img domain.BaseImage
	var sourceType, state, createdAt string
	var projectScope, sourceSnapshotID, backendRef, meta sql.NullString

	err := rows.Scan(&img.ImageID, &projectScope, &img.Name, &img.Version,
		&sourceType, &sourceSnapshotID, &img.Backend, &backendRef,
		&img.Architecture, &state, &createdAt, &meta)
	if err != nil {
		return nil, err
	}
	populateImage(&img, sourceType, state, createdAt, projectScope, sourceSnapshotID, backendRef, meta)
	return &img, nil
}

func populateImage(img *domain.BaseImage, sourceType, state, createdAt string,
	projectScope, sourceSnapshotID, backendRef, meta sql.NullString) {
	img.SourceType = domain.SourceType(sourceType)
	img.State = domain.ImageState(state)
	img.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if projectScope.Valid {
		img.ProjectScope = projectScope.String
	}
	if sourceSnapshotID.Valid {
		img.SourceSnapshotID = sourceSnapshotID.String
	}
	if backendRef.Valid {
		img.BackendRef = backendRef.String
	}
	if meta.Valid {
		json.Unmarshal([]byte(meta.String), &img.Metadata)
	}
}
