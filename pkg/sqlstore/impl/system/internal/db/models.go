// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.13.0

package db

import (
	"database/sql"
	"time"

	"github.com/jackc/pgtype"
)

type Registry struct {
	CreatedAt  time.Time
	ID         pgtype.Numeric
	Structure  string
	Controller string
	Prefix     string
	ChainID    int64
}

type SystemAcl struct {
	TableID    pgtype.Numeric
	Controller string
	Privileges []string
	CreatedAt  time.Time
	UpdatedAt  sql.NullTime
	ChainID    int64
}

type SystemAuth struct {
	Address          string
	CreatedAt        time.Time
	LastSeen         sql.NullTime
	CreateTableCount int32
	RunSqlCount      int32
}

type SystemController struct {
	ChainID    int64
	TableID    pgtype.Numeric
	Controller string
}

type SystemPendingTx struct {
	ChainID   int64
	Address   string
	Hash      string
	Nonce     int64
	CreatedAt time.Time
}

type SystemTxnProcessor struct {
	BlockNumber sql.NullInt64
	ChainID     int64
}

type SystemTxnReceipt struct {
	ID          sql.NullInt64
	ChainID     int64
	BlockNumber int64
	TxnHash     string
	Error       sql.NullString
	TableID     pgtype.Numeric
}
