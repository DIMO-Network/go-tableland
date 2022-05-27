// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.13.0
// source: nonce.sql

package db

import (
	"context"
)

const deletePendingTxByHash = `-- name: DeletePendingTxByHash :exec
DELETE FROM system_pending_tx WHERE chain_id=$1 AND hash = $2
`

type DeletePendingTxByHashParams struct {
	ChainID int64
	Hash    string
}

func (q *Queries) DeletePendingTxByHash(ctx context.Context, arg DeletePendingTxByHashParams) error {
	_, err := q.db.Exec(ctx, deletePendingTxByHash, arg.ChainID, arg.Hash)
	return err
}

const insertPendingTx = `-- name: InsertPendingTx :exec
INSERT INTO system_pending_tx ("chain_id", "address", "hash", "nonce") VALUES ($1, $2, $3, $4)
`

type InsertPendingTxParams struct {
	ChainID int64
	Address string
	Hash    string
	Nonce   int64
}

func (q *Queries) InsertPendingTx(ctx context.Context, arg InsertPendingTxParams) error {
	_, err := q.db.Exec(ctx, insertPendingTx,
		arg.ChainID,
		arg.Address,
		arg.Hash,
		arg.Nonce,
	)
	return err
}

const listPendingTx = `-- name: ListPendingTx :many
SELECT chain_id, address, hash, nonce, created_at FROM system_pending_tx WHERE address = $1 AND chain_id = $2 order by nonce
`

type ListPendingTxParams struct {
	Address string
	ChainID int64
}

func (q *Queries) ListPendingTx(ctx context.Context, arg ListPendingTxParams) ([]SystemPendingTx, error) {
	rows, err := q.db.Query(ctx, listPendingTx, arg.Address, arg.ChainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SystemPendingTx
	for rows.Next() {
		var i SystemPendingTx
		if err := rows.Scan(
			&i.ChainID,
			&i.Address,
			&i.Hash,
			&i.Nonce,
			&i.CreatedAt,
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
