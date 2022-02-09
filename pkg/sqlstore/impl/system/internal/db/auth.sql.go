// Code generated by sqlc. DO NOT EDIT.
// source: auth.sql

package db

import (
	"context"
)

const authorize = `-- name: Authorize :exec
INSERT INTO system_auth ("address") VALUES ($1)
`

func (q *Queries) Authorize(ctx context.Context, address string) error {
	_, err := q.db.Exec(ctx, authorize, address)
	return err
}

const getAuthorized = `-- name: GetAuthorized :one
SELECT address, created_at, last_seen, create_table_count, run_sql_count FROM system_auth WHERE address ILIKE $1
`

func (q *Queries) GetAuthorized(ctx context.Context, address string) (SystemAuth, error) {
	row := q.db.QueryRow(ctx, getAuthorized, address)
	var i SystemAuth
	err := row.Scan(
		&i.Address,
		&i.CreatedAt,
		&i.LastSeen,
		&i.CreateTableCount,
		&i.RunSqlCount,
	)
	return i, err
}

const incrementCreateTableCount = `-- name: IncrementCreateTableCount :exec
UPDATE system_auth SET create_table_count = create_table_count+1, last_seen = NOW() WHERE address ILIKE $1
`

func (q *Queries) IncrementCreateTableCount(ctx context.Context, address string) error {
	_, err := q.db.Exec(ctx, incrementCreateTableCount, address)
	return err
}

const incrementRunSQLCount = `-- name: IncrementRunSQLCount :exec
UPDATE system_auth SET run_sql_count = run_sql_count+1, last_seen = NOW() WHERE address ILIKE $1
`

func (q *Queries) IncrementRunSQLCount(ctx context.Context, address string) error {
	_, err := q.db.Exec(ctx, incrementRunSQLCount, address)
	return err
}

const isAuthorized = `-- name: IsAuthorized :one
SELECT EXISTS(SELECT 1 from system_auth WHERE address ILIKE $1) AS "exists"
`

func (q *Queries) IsAuthorized(ctx context.Context, address string) (bool, error) {
	row := q.db.QueryRow(ctx, isAuthorized, address)
	var exists bool
	err := row.Scan(&exists)
	return exists, err
}

const listAuthorized = `-- name: ListAuthorized :many
SELECT address, created_at, last_seen, create_table_count, run_sql_count FROM system_auth ORDER BY created_at ASC
`

func (q *Queries) ListAuthorized(ctx context.Context) ([]SystemAuth, error) {
	rows, err := q.db.Query(ctx, listAuthorized)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SystemAuth
	for rows.Next() {
		var i SystemAuth
		if err := rows.Scan(
			&i.Address,
			&i.CreatedAt,
			&i.LastSeen,
			&i.CreateTableCount,
			&i.RunSqlCount,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const revoke = `-- name: Revoke :exec
DELETE FROM system_auth WHERE address ILIKE $1
`

func (q *Queries) Revoke(ctx context.Context, address string) error {
	_, err := q.db.Exec(ctx, revoke, address)
	return err
}
