package user

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/textileio/go-tableland/pkg/sqlstore"
)

func rowsToJSON(rows *sql.Rows) (*sqlstore.UserRows, error) {
	columns, err := getColumnsData(rows)
	if err != nil {
		return nil, fmt.Errorf("get columns from rows: %s", err)
	}
	rowsData, err := getRowsData(rows, len(columns))
	if err != nil {
		return nil, err
	}

	return &sqlstore.UserRows{
		Columns: columns,
		Rows:    rowsData,
	}, nil
}

func getColumnsData(rows *sql.Rows) ([]sqlstore.UserColumn, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("get columns from sql.Rows: %s", err)
	}
	columns := make([]sqlstore.UserColumn, len(cols))
	for i := range cols {
		columns[i] = sqlstore.UserColumn{Name: cols[i]}
	}
	return columns, nil
}

func getRowsData(rows *sql.Rows, numColumns int) ([][]interface{}, error) {
	rowsData := make([][]interface{}, 0)
	for rows.Next() {
		scanArgs := make([]interface{}, numColumns)
		for i := range scanArgs {
			scanArgs[i] = new(interface{})
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, fmt.Errorf("scan row column: %s", err)
		}
		for i, scanArg := range scanArgs {
			switch src := (*scanArg.(*interface{})).(type) {
			case string:
				trimmed := strings.TrimLeft(src, " ")
				if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
					scanArgs[i] = []byte(trimmed)
				}
			case []byte:
				tmp := make([]byte, len(src))
				copy(tmp, src)
				scanArgs[i] = tmp
			}
		}
		rowsData = append(rowsData, scanArgs)
	}

	return rowsData, nil
}
