package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

type portBindingStore struct {
	db *sql.DB
}

func (s *Store) PortBindingStore() domain.PortBindingStore {
	return &portBindingStore{db: s.db}
}

func (ps *portBindingStore) Create(ctx context.Context, pb *domain.PortBinding) error {
	_, err := ps.db.ExecContext(ctx, `INSERT INTO port_bindings
		(sandbox_id, target_port, published_port, host_address, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		pb.SandboxID, pb.TargetPort, pb.PublishedPort, pb.HostAddress,
		pb.CreatedAt.Format(time.RFC3339Nano))
	return mapError(err)
}

func (ps *portBindingStore) ListBySandbox(ctx context.Context, sandboxID string) ([]*domain.PortBinding, error) {
	rows, err := ps.db.QueryContext(ctx, `SELECT
		sandbox_id, target_port, published_port, host_address, created_at
		FROM port_bindings WHERE sandbox_id = ? ORDER BY target_port`, sandboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bindings []*domain.PortBinding
	for rows.Next() {
		pb, err := scanPortBindingRow(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, pb)
	}
	return bindings, rows.Err()
}

func (ps *portBindingStore) Delete(ctx context.Context, sandboxID string, targetPort int) error {
	res, err := ps.db.ExecContext(ctx,
		`DELETE FROM port_bindings WHERE sandbox_id = ? AND target_port = ?`,
		sandboxID, targetPort)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

func (ps *portBindingStore) GetByPublishedPort(ctx context.Context, publishedPort int) (*domain.PortBinding, error) {
	row := ps.db.QueryRowContext(ctx, `SELECT
		sandbox_id, target_port, published_port, host_address, created_at
		FROM port_bindings WHERE published_port = ?`, publishedPort)
	var pb domain.PortBinding
	var createdAt string
	err := row.Scan(&pb.SandboxID, &pb.TargetPort, &pb.PublishedPort, &pb.HostAddress, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("port binding: %w", domain.ErrNotFound)
		}
		return nil, err
	}
	pb.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &pb, nil
}

func (ps *portBindingStore) NextAvailablePort(ctx context.Context, rangeStart, rangeEnd int) (int, error) {
	// Find the lowest port in [rangeStart, rangeEnd] not already used
	row := ps.db.QueryRowContext(ctx, `
		WITH RECURSIVE candidates(port) AS (
			SELECT ?
			UNION ALL
			SELECT port + 1 FROM candidates WHERE port < ?
		)
		SELECT port FROM candidates
		WHERE port NOT IN (SELECT published_port FROM port_bindings)
		LIMIT 1`, rangeStart, rangeEnd)
	var port int
	err := row.Scan(&port)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("port range exhausted: %w", domain.ErrCapacityExceeded)
		}
		return 0, err
	}
	return port, nil
}

func scanPortBindingRow(rows *sql.Rows) (*domain.PortBinding, error) {
	var pb domain.PortBinding
	var createdAt string
	err := rows.Scan(&pb.SandboxID, &pb.TargetPort, &pb.PublishedPort, &pb.HostAddress, &createdAt)
	if err != nil {
		return nil, err
	}
	pb.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &pb, nil
}
