// Code generated by sqlc. DO NOT EDIT.
// source: acl.sql

package db

import (
	"context"

	"github.com/jackc/pgtype"
)

const getAclByTableAndController = `-- name: GetAclByTableAndController :one
SELECT table_id, controller, privileges, created_at, updated_at FROM system_acl WHERE table_id = $2 and controller ILIKE $1
`

type GetAclByTableAndControllerParams struct {
	Controller string
	TableID    pgtype.Numeric
}

func (q *Queries) GetAclByTableAndController(ctx context.Context, arg GetAclByTableAndControllerParams) (SystemAcl, error) {
	row := q.db.QueryRow(ctx, getAclByTableAndController, arg.Controller, arg.TableID)
	var i SystemAcl
	err := row.Scan(
		&i.TableID,
		&i.Controller,
		&i.Privileges,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}