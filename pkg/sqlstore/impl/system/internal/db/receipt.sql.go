// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.13.0
// source: receipt.sql

package db

import (
	"context"
)

const getReceipt = `-- name: GetReceipt :one
SELECT id, chain_id, block_number, txn_hash, error, table_id from system_txn_receipts WHERE chain_id=$1 and txn_hash=$2
`

type GetReceiptParams struct {
	ChainID int64
	TxnHash string
}

func (q *Queries) GetReceipt(ctx context.Context, arg GetReceiptParams) (SystemTxnReceipt, error) {
	row := q.queryRow(ctx, q.getReceiptStmt, getReceipt, arg.ChainID, arg.TxnHash)
	var i SystemTxnReceipt
	err := row.Scan(
		&i.ID,
		&i.ChainID,
		&i.BlockNumber,
		&i.TxnHash,
		&i.Error,
		&i.TableID,
	)
	return i, err
}
