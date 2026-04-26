package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

type boostStore struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

// BoostStore is the boost-row accessor. Wired into *Store so the top-level
// store.Store interface (in internal/store/store.go) is satisfied.
func (s *Store) BoostStore() domain.BoostStore {
	return &boostStore{readDB: s.readDB, writeDB: s.writeDB}
}

func (bs *boostStore) Upsert(ctx context.Context, b *domain.Boost) error {
	_, err := bs.writeDB.ExecContext(ctx, `INSERT INTO boosts
		(boost_id, sandbox_id, original_cpu_limit, original_memory_limit_mb,
		 boosted_cpu_limit, boosted_memory_limit_mb,
		 started_at, expires_at, state, revert_attempts, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sandbox_id) DO UPDATE SET
			boost_id=excluded.boost_id,
			original_cpu_limit=excluded.original_cpu_limit,
			original_memory_limit_mb=excluded.original_memory_limit_mb,
			boosted_cpu_limit=excluded.boosted_cpu_limit,
			boosted_memory_limit_mb=excluded.boosted_memory_limit_mb,
			started_at=excluded.started_at,
			expires_at=excluded.expires_at,
			state=excluded.state,
			revert_attempts=excluded.revert_attempts,
			last_error=excluded.last_error`,
		b.BoostID, b.SandboxID,
		nullInt(b.OriginalCPULimit), nullInt(b.OriginalMemoryLimitMB),
		nullInt(b.BoostedCPULimit), nullInt(b.BoostedMemoryLimitMB),
		b.StartedAt.Format(time.RFC3339Nano),
		b.ExpiresAt.Format(time.RFC3339Nano),
		string(b.State), b.RevertAttempts, b.LastError)
	return mapError(err)
}

func (bs *boostStore) Get(ctx context.Context, sandboxID string) (*domain.Boost, error) {
	row := bs.readDB.QueryRowContext(ctx, boostSelect+` WHERE sandbox_id = ?`, sandboxID)
	return scanBoost(row)
}

func (bs *boostStore) GetByID(ctx context.Context, boostID string) (*domain.Boost, error) {
	row := bs.readDB.QueryRowContext(ctx, boostSelect+` WHERE boost_id = ?`, boostID)
	return scanBoost(row)
}

func (bs *boostStore) UpdateState(ctx context.Context, boostID string, state domain.BoostState, attempts int, lastErr string) error {
	res, err := bs.writeDB.ExecContext(ctx,
		`UPDATE boosts SET state=?, revert_attempts=?, last_error=? WHERE boost_id=?`,
		string(state), attempts, lastErr, boostID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (bs *boostStore) Delete(ctx context.Context, boostID string) error {
	res, err := bs.writeDB.ExecContext(ctx, `DELETE FROM boosts WHERE boost_id = ?`, boostID)
	if err != nil {
		return mapError(err)
	}
	return checkRowsAffected(res)
}

func (bs *boostStore) ListAll(ctx context.Context) ([]*domain.Boost, error) {
	rows, err := bs.readDB.QueryContext(ctx, boostSelect)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*domain.Boost
	for rows.Next() {
		b, err := scanBoostRow(rows)
		if err != nil {
			return nil, mapError(err)
		}
		out = append(out, b)
	}
	return out, mapError(rows.Err())
}

const boostSelect = `SELECT boost_id, sandbox_id,
	original_cpu_limit, original_memory_limit_mb,
	boosted_cpu_limit, boosted_memory_limit_mb,
	started_at, expires_at, state, revert_attempts, last_error
	FROM boosts`

type boostScannable interface {
	Scan(dst ...any) error
}

func scanBoost(row *sql.Row) (*domain.Boost, error) {
	return scanBoostFrom(row)
}

func scanBoostRow(rows *sql.Rows) (*domain.Boost, error) {
	return scanBoostFrom(rows)
}

func scanBoostFrom(s boostScannable) (*domain.Boost, error) {
	var (
		b       domain.Boost
		origCPU sql.NullInt64
		origMem sql.NullInt64
		bstCPU  sql.NullInt64
		bstMem  sql.NullInt64
		started string
		expires string
		state   string
		lastErr sql.NullString
	)
	err := s.Scan(&b.BoostID, &b.SandboxID,
		&origCPU, &origMem, &bstCPU, &bstMem,
		&started, &expires, &state, &b.RevertAttempts, &lastErr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, mapError(err)
	}
	if origCPU.Valid {
		v := int(origCPU.Int64)
		b.OriginalCPULimit = &v
	}
	if origMem.Valid {
		v := int(origMem.Int64)
		b.OriginalMemoryLimitMB = &v
	}
	if bstCPU.Valid {
		v := int(bstCPU.Int64)
		b.BoostedCPULimit = &v
	}
	if bstMem.Valid {
		v := int(bstMem.Int64)
		b.BoostedMemoryLimitMB = &v
	}
	if t, perr := time.Parse(time.RFC3339Nano, started); perr == nil {
		b.StartedAt = t
	}
	if t, perr := time.Parse(time.RFC3339Nano, expires); perr == nil {
		b.ExpiresAt = t
	}
	b.State = domain.BoostState(state)
	if lastErr.Valid {
		b.LastError = lastErr.String
	}
	return &b, nil
}
