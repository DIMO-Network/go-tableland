package impl

import (
	"context"
	"crypto/ecdsa"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/abi/bind/backends"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"github.com/textileio/go-tableland/internal/chains"
	"github.com/textileio/go-tableland/internal/router/middlewares"
	"github.com/textileio/go-tableland/internal/tableland"
	"github.com/textileio/go-tableland/pkg/eventprocessor/eventfeed"
	efimpl "github.com/textileio/go-tableland/pkg/eventprocessor/eventfeed/impl"
	epimpl "github.com/textileio/go-tableland/pkg/eventprocessor/impl"
	executor "github.com/textileio/go-tableland/pkg/eventprocessor/impl/executor/impl"
	"github.com/textileio/go-tableland/pkg/nonce/impl"
	"github.com/textileio/go-tableland/pkg/parsing"
	parserimpl "github.com/textileio/go-tableland/pkg/parsing/impl"
	"github.com/textileio/go-tableland/pkg/sqlstore"
	"github.com/textileio/go-tableland/pkg/sqlstore/impl/system"
	"github.com/textileio/go-tableland/pkg/sqlstore/impl/user"
	"github.com/textileio/go-tableland/pkg/tables/impl/ethereum"
	"github.com/textileio/go-tableland/pkg/tables/impl/testutil"
	"github.com/textileio/go-tableland/pkg/wallet"
	"github.com/textileio/go-tableland/tests"
)

func TestTodoAppWorkflow(t *testing.T) {
	t.Parallel()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(true).
		build(t)
	tablelandClient := setup.newTablelandClient(t)

	ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
	tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts

	caller := txOpts.From
	_, err := sc.CreateTable(txOpts, caller,
		`CREATE TABLE todoapp_1337 (
			complete INTEGER DEFAULT 0,
			name     TEXT DEFAULT '',
			deleted  INTEGER DEFAULT 0,
			id       INTEGER
		  );`)
	require.NoError(t, err)

	processCSV(ctx, t, caller, tbld, "testdata/todoapp_queries.csv", backend)
}

func TestAny(t *testing.T) {
	t.Parallel()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(true).
		build(t)
	tablelandClient := setup.newTablelandClient(t)

	ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
	tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts

	caller := txOpts.From
	_, err := sc.CreateTable(txOpts, caller,
		`CREATE TABLE any_1337 (
			wild ANY
		  );`)
	require.NoError(t, err)

	processCSV(ctx, t, caller, tbld, "testdata/any_queries.csv", backend)
}

func TestInsertOnConflict(t *testing.T) {
	t.Parallel()
	// TODO: This test was passing because the "DO UPDATE SET" clause didn't have a table name.
	//       It's disabled temporarily until some soon related work in the validator will fix this.
	t.SkipNow()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(true).
		build(t)
	tablelandClient := setup.newTablelandClient(t)

	ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
	tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts

	caller := txOpts.From

	_, err := sc.CreateTable(txOpts, caller,
		`CREATE TABLE foo_1337 (
			name text unique,
			count int);`)
	require.NoError(t, err)
	backend.Commit()

	ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller.Hex())
	baseReq := tableland.RelayWriteQueryRequest{}
	req := baseReq
	var txnHashes []string
	for i := 0; i < 10; i++ {
		req.Statement = `INSERT INTO foo_1337_1 VALUES ('bar', 0) ON CONFLICT (name) DO UPDATE SET count=_1.count+1`
		r, err := tbld.RelayWriteQuery(ctx, req)
		require.NoError(t, err)
		backend.Commit()
		txnHashes = append(txnHashes, r.Transaction.Hash)
	}

	readReq := tableland.RunReadQueryRequest{Statement: "SELECT count FROM foo_1337_1"}
	require.Eventually(
		t,
		jsonEq(ctx, t, tbld, readReq, `{"columns":[{"name":"count"}],"rows":[[9]]}`),
		time.Second*5,
		time.Millisecond*100,
	)
	requireReceipts(ctx, t, tbld, txnHashes, true)
}

func TestMultiStatement(t *testing.T) {
	t.Parallel()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(true).
		build(t)
	tablelandClient := setup.newTablelandClient(t)

	ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
	tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts
	caller := txOpts.From

	_, err := sc.CreateTable(txOpts, caller,
		`CREATE TABLE foo_1337 (
			name text unique
		);`)
	require.NoError(t, err)

	ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller.Hex())
	req := tableland.RelayWriteQueryRequest{
		Statement: `INSERT INTO foo_1337_1 values ('bar'); UPDATE foo_1337_1 SET name='zoo'`,
	}
	r, err := tbld.RelayWriteQuery(ctx, req)
	require.NoError(t, err)
	backend.Commit()

	readReq := tableland.RunReadQueryRequest{Statement: "SELECT name from foo_1337_1"}
	require.Eventually(
		t,
		jsonEq(ctx, t, tbld, readReq, `{"columns":[{"name":"name"}],"rows":[["zoo"]]}`),
		time.Second*5,
		time.Millisecond*100,
	)
	requireReceipts(ctx, t, tbld, []string{r.Transaction.Hash}, true)
}

func TestReadSystemTable(t *testing.T) {
	t.Parallel()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(true).
		build(t)
	tablelandClient := setup.newTablelandClient(t)

	ctx, sc := setup.ctx, setup.contract
	tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts
	caller := txOpts.From

	_, err := sc.CreateTable(txOpts, caller, `CREATE TABLE foo_1337 (myjson TEXT);`)
	require.NoError(t, err)

	res, err := runReadQuery(ctx, t, tbld, "select * from registry", caller.Hex())
	require.NoError(t, err)
	_, err = json.Marshal(res.Result)
	require.NoError(t, err)
}

func TestJSON(t *testing.T) {
	t.Parallel()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(true).
		build(t)
	tablelandClient := setup.newTablelandClient(t)

	ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
	tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts
	caller := txOpts.From

	_, err := sc.CreateTable(txOpts, caller, `CREATE TABLE foo_1337 (myjson TEXT);`)
	require.NoError(t, err)

	processCSV(ctx, t, caller, tbld, "testdata/json_queries.csv", backend)
}

func TestCheckInsertPrivileges(t *testing.T) {
	t.Parallel()

	type testCase struct { // nolint
		query      string
		privileges tableland.Privileges
		isAllowed  bool
	}

	tests := []testCase{
		{"INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", tableland.Privileges{}, false},
		{"INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", tableland.Privileges{tableland.PrivInsert}, true},
		{"INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", tableland.Privileges{tableland.PrivUpdate}, false},
		{"INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", tableland.Privileges{tableland.PrivDelete}, false},
		{"INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", tableland.Privileges{tableland.PrivInsert, tableland.PrivUpdate}, true},                       // nolint
		{"INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", tableland.Privileges{tableland.PrivInsert, tableland.PrivDelete}, true},                       // nolint
		{"INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", tableland.Privileges{tableland.PrivUpdate, tableland.PrivDelete}, false},                      // nolint
		{"INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", tableland.Privileges{tableland.PrivInsert, tableland.PrivUpdate, tableland.PrivDelete}, true}, // nolint
	}

	for i, test := range tests {
		t.Run(fmt.Sprint(i+1), func(test testCase) func(t *testing.T) {
			return func(t *testing.T) {
				t.Parallel()

				setup := newTablelandSetupBuilder().
					withAllowTransactionRelay(true).
					build(t)

				granterSetup := setup.newTablelandClient(t)
				granteeSetup := setup.newTablelandClient(t)

				ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
				tbldGranter, txOptsGranter := granterSetup.tableland, granterSetup.txOpts
				tbldGrantee, txOptsGrantee := granteeSetup.tableland, granteeSetup.txOpts

				granter := txOptsGranter.From.Hex()
				grantee := txOptsGrantee.From.Hex()

				_, err := sc.CreateTable(txOptsGranter, common.HexToAddress(granter), `CREATE TABLE foo_1337 (bar text);`)
				require.NoError(t, err)
				backend.Commit()

				var successfulTxnHashes []string
				if len(test.privileges) > 0 {
					privileges := make([]string, len(test.privileges))
					for i, priv := range test.privileges {
						privileges[i] = priv.ToSQLString()
					}

					// execute grant statement according to test case
					grantQuery := fmt.Sprintf("GRANT %s ON foo_1337_1 TO '%s'", strings.Join(privileges, ","), grantee)
					r, err := relayWriteQuery(ctx, t, tbldGranter, grantQuery, granter)
					require.NoError(t, err)
					backend.Commit()
					successfulTxnHashes = append(successfulTxnHashes, r.Transaction.Hash)
				}

				r, err := relayWriteQuery(ctx, t, tbldGrantee, test.query, grantee)
				require.NoError(t, err)
				backend.Commit()

				testQuery := "SELECT * FROM foo_1337_1 WHERE bar ='Hello';"
				if test.isAllowed {
					require.Eventually(t,
						runSQLCountEq(ctx, t, tbldGrantee, testQuery, grantee, 1),
						5*time.Second,
						100*time.Millisecond,
					)
					successfulTxnHashes = append(successfulTxnHashes, r.Transaction.Hash)
					requireReceipts(ctx, t, tbldGrantee, successfulTxnHashes, true)
				} else {
					require.Never(t, runSQLCountEq(ctx, t, tbldGrantee, testQuery, grantee, 1), 5*time.Second, 100*time.Millisecond)
					requireReceipts(ctx, t, tbldGrantee, successfulTxnHashes, true)
					requireReceipts(ctx, t, tbldGrantee, []string{r.Transaction.Hash}, false)
				}
			}
		}(test))
	}
}

func TestCheckUpdatePrivileges(t *testing.T) {
	t.Parallel()

	type testCase struct { // nolint
		query      string
		privileges tableland.Privileges
		isAllowed  bool
	}

	tests := []testCase{
		{"UPDATE foo_1337_1 SET bar = 'Hello 2'", tableland.Privileges{}, false},
		{"UPDATE foo_1337_1 SET bar = 'Hello 2'", tableland.Privileges{tableland.PrivInsert}, false},
		{"UPDATE foo_1337_1 SET bar = 'Hello 2'", tableland.Privileges{tableland.PrivUpdate}, true},
		{"UPDATE foo_1337_1 SET bar = 'Hello 2'", tableland.Privileges{tableland.PrivDelete}, false},
		{"UPDATE foo_1337_1 SET bar = 'Hello 2'", tableland.Privileges{tableland.PrivInsert, tableland.PrivUpdate}, true},
		{"UPDATE foo_1337_1 SET bar = 'Hello 2'", tableland.Privileges{tableland.PrivInsert, tableland.PrivDelete}, false},
		{"UPDATE foo_1337_1 SET bar = 'Hello 2'", tableland.Privileges{tableland.PrivUpdate, tableland.PrivDelete}, true},
		{"UPDATE foo_1337_1 SET bar = 'Hello 2'", tableland.Privileges{tableland.PrivInsert, tableland.PrivUpdate, tableland.PrivDelete}, true}, // nolint
	}

	for i, test := range tests {
		t.Run(fmt.Sprint(i+1), func(test testCase) func(t *testing.T) {
			return func(t *testing.T) {
				t.Parallel()

				setup := newTablelandSetupBuilder().
					withAllowTransactionRelay(true).
					build(t)

				granterSetup := setup.newTablelandClient(t)
				granteeSetup := setup.newTablelandClient(t)

				ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
				tbldGranter, txOptsGranter := granterSetup.tableland, granterSetup.txOpts
				tbldGrantee, txOptsGrantee := granteeSetup.tableland, granteeSetup.txOpts

				granter := txOptsGranter.From.Hex()
				grantee := txOptsGrantee.From.Hex()

				_, err := sc.CreateTable(txOptsGranter, common.HexToAddress(granter), `CREATE TABLE foo_1337 (bar text);`)
				require.NoError(t, err)
				backend.Commit()
				var successfulTxnHashes []string

				// we initilize the table with a row to be updated
				r, err := relayWriteQuery(ctx, t, tbldGranter, "INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", granter) // nolint
				require.NoError(t, err)
				backend.Commit()
				successfulTxnHashes = append(successfulTxnHashes, r.Transaction.Hash)

				if len(test.privileges) > 0 {
					privileges := make([]string, len(test.privileges))
					for i, priv := range test.privileges {
						privileges[i] = priv.ToSQLString()
					}

					// execute grant statement according to test case
					grantQuery := fmt.Sprintf("GRANT %s ON foo_1337_1 TO '%s'", strings.Join(privileges, ","), grantee)
					r, err := relayWriteQuery(ctx, t, tbldGranter, grantQuery, granter)
					require.NoError(t, err)
					backend.Commit()
					successfulTxnHashes = append(successfulTxnHashes, r.Transaction.Hash)
				}

				r, err = relayWriteQuery(ctx, t, tbldGrantee, test.query, grantee)
				require.NoError(t, err)
				backend.Commit()

				testQuery := "SELECT * FROM foo_1337_1 WHERE bar='Hello 2';"
				if test.isAllowed {
					require.Eventually(t,
						runSQLCountEq(ctx, t, tbldGrantee, testQuery, grantee, 1),
						5*time.Second,
						100*time.Millisecond,
					)
					successfulTxnHashes = append(successfulTxnHashes, r.Transaction.Hash)
					requireReceipts(ctx, t, tbldGrantee, successfulTxnHashes, true)
				} else {
					require.Never(t, runSQLCountEq(ctx, t, tbldGrantee, testQuery, grantee, 1), 5*time.Second, 100*time.Millisecond)
					requireReceipts(ctx, t, tbldGrantee, successfulTxnHashes, true)
					requireReceipts(ctx, t, tbldGrantee, []string{r.Transaction.Hash}, false)
				}
			}
		}(test))
	}
}

func TestCheckDeletePrivileges(t *testing.T) {
	t.Parallel()

	type testCase struct { // nolint
		query      string
		privileges tableland.Privileges
		isAllowed  bool
	}

	tests := []testCase{
		{"DELETE FROM foo_1337_1", tableland.Privileges{}, false},
		{"DELETE FROM foo_1337_1", tableland.Privileges{tableland.PrivInsert}, false},
		{"DELETE FROM foo_1337_1", tableland.Privileges{tableland.PrivUpdate}, false},
		{"DELETE FROM foo_1337_1", tableland.Privileges{tableland.PrivDelete}, true},
		{"DELETE FROM foo_1337_1", tableland.Privileges{tableland.PrivInsert, tableland.PrivUpdate}, false},
		{"DELETE FROM foo_1337_1", tableland.Privileges{tableland.PrivInsert, tableland.PrivDelete}, true},
		{"DELETE FROM foo_1337_1", tableland.Privileges{tableland.PrivUpdate, tableland.PrivDelete}, true},
		{"DELETE FROM foo_1337_1", tableland.Privileges{tableland.PrivInsert, tableland.PrivUpdate, tableland.PrivDelete}, true}, // nolint
	}

	for i, test := range tests {
		t.Run(fmt.Sprint(i+1), func(test testCase) func(t *testing.T) {
			return func(t *testing.T) {
				t.Parallel()

				setup := newTablelandSetupBuilder().
					withAllowTransactionRelay(true).
					build(t)

				granterSetup := setup.newTablelandClient(t)
				granteeSetup := setup.newTablelandClient(t)

				ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
				tbldGranter, txOptsGranter := granterSetup.tableland, granterSetup.txOpts
				tbldGrantee, txOptsGrantee := granteeSetup.tableland, granteeSetup.txOpts

				granter := txOptsGranter.From.Hex()
				grantee := txOptsGrantee.From.Hex()

				_, err := sc.CreateTable(txOptsGranter, common.HexToAddress(granter), `CREATE TABLE foo_1337 (bar text);`)
				require.NoError(t, err)
				var successfulTxnHashes []string

				// we initilize the table with a row to be delete
				_, err = relayWriteQuery(ctx, t, tbldGranter, "INSERT INTO foo_1337_1 (bar) VALUES ('Hello')", granter) // nolint
				require.NoError(t, err)
				backend.Commit()

				if len(test.privileges) > 0 {
					privileges := make([]string, len(test.privileges))
					for i, priv := range test.privileges {
						privileges[i] = priv.ToSQLString()
					}

					// execute grant statement according to test case
					grantQuery := fmt.Sprintf("GRANT %s ON foo_1337_1 TO '%s'", strings.Join(privileges, ","), grantee)
					r, err := relayWriteQuery(ctx, t, tbldGranter, grantQuery, granter)
					require.NoError(t, err)
					backend.Commit()
					successfulTxnHashes = append(successfulTxnHashes, r.Transaction.Hash)
				}

				r, err := relayWriteQuery(ctx, t, tbldGrantee, test.query, grantee)
				require.NoError(t, err)
				backend.Commit()

				testQuery := "SELECT * FROM foo_1337_1"
				if test.isAllowed {
					require.Eventually(t,
						runSQLCountEq(ctx, t, tbldGrantee, testQuery, grantee, 0),
						5*time.Second,
						100*time.Millisecond,
					)
					successfulTxnHashes = append(successfulTxnHashes, r.Transaction.Hash)
					requireReceipts(ctx, t, tbldGrantee, successfulTxnHashes, true)
				} else {
					require.Never(t, runSQLCountEq(ctx, t, tbldGrantee, testQuery, grantee, 0), 5*time.Second, 100*time.Millisecond)
					requireReceipts(ctx, t, tbldGrantee, []string{r.Transaction.Hash}, false)
				}
			}
		}(test))
	}
}

func TestOwnerRevokesItsPrivilegeInsideMultipleStatements(t *testing.T) {
	t.Parallel()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(true).
		build(t)
	tablelandClient := setup.newTablelandClient(t)

	ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
	tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts
	caller := txOpts.From.Hex()

	_, err := sc.CreateTable(txOpts, common.HexToAddress(caller), `CREATE TABLE foo_1337 (bar text);`)
	require.NoError(t, err)

	multiStatements := `
		INSERT INTO foo_1337_1 (bar) VALUES ('Hello');
		UPDATE foo_1337_1 SET bar = 'Hello 2';
		REVOKE update ON foo_1337_1 FROM '` + caller + `';
		UPDATE foo_1337_1 SET bar = 'Hello 3';
	`
	r, err := relayWriteQuery(ctx, t, tbld, multiStatements, caller)
	require.NoError(t, err)
	backend.Commit()

	testQuery := "SELECT * FROM foo_1337_1;"
	cond := runSQLCountEq(ctx, t, tbld, testQuery, caller, 1)
	require.Never(t, cond, 5*time.Second, 100*time.Millisecond)
	requireReceipts(ctx, t, tbld, []string{r.Transaction.Hash}, false)
}

func TestTransferTable(t *testing.T) {
	t.Parallel()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(true).
		build(t)

	owner1Setup := setup.newTablelandClient(t)
	owner2Setup := setup.newTablelandClient(t)

	ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
	tbldOwner1, txOptsOwner1 := owner1Setup.tableland, owner1Setup.txOpts
	tbldOwner2, txOptsOwner2 := owner2Setup.tableland, owner2Setup.txOpts

	_, err := sc.CreateTable(txOptsOwner1, txOptsOwner1.From, `CREATE TABLE foo_1337 (bar text);`)
	require.NoError(t, err)

	// transfer table from owner1 to owner2
	_, err = sc.TransferFrom(txOptsOwner1, txOptsOwner1.From, txOptsOwner2.From, big.NewInt(1))
	require.NoError(t, err)

	// we'll execute one insert with owner1 and one insert with owner2
	query1 := "INSERT INTO foo_1337_1 (bar) VALUES ('Hello')"
	r1, err := relayWriteQuery(ctx, t, tbldOwner1, query1, txOptsOwner1.From.Hex())
	require.NoError(t, err)
	backend.Commit()

	query2 := "INSERT INTO foo_1337_1 (bar) VALUES ('Hello2')"
	r2, err := relayWriteQuery(ctx, t, tbldOwner2, query2, txOptsOwner2.From.Hex())
	require.NoError(t, err)
	backend.Commit()

	// insert from owner1 will NEVER go through
	require.Never(t,
		runSQLCountEq(ctx, t, tbldOwner1, "SELECT * FROM foo_1337_1 WHERE bar ='Hello';", txOptsOwner1.From.Hex(), 1),
		5*time.Second,
		100*time.Millisecond,
	)
	requireReceipts(ctx, t, tbldOwner1, []string{r1.Transaction.Hash}, false)

	// insert from owner2 will EVENTUALLY go through
	require.Eventually(t,
		runSQLCountEq(ctx, t, tbldOwner2, "SELECT * FROM foo_1337_1 WHERE bar ='Hello2';", txOptsOwner2.From.Hex(), 1),
		5*time.Second,
		100*time.Millisecond,
	)
	requireReceipts(ctx, t, tbldOwner2, []string{r2.Transaction.Hash}, true)

	// check registry table new ownership
	require.Eventually(t,
		runSQLCountEq(ctx,
			t,
			tbldOwner2,
			fmt.Sprintf("SELECT * FROM registry WHERE controller = '%s' AND id = 1 AND chain_id = 1337", txOptsOwner2.From.Hex()), // nolint
			txOptsOwner2.From.Hex(),
			1,
		),
		5*time.Second,
		100*time.Millisecond,
	)
}

func TestQueryConstraints(t *testing.T) {
	t.Parallel()

	t.Run("write-query-size-ok", func(t *testing.T) {
		t.Parallel()

		parsingOpts := []parsing.Option{
			parsing.WithMaxWriteQuerySize(45),
		}

		setup := newTablelandSetupBuilder().
			withAllowTransactionRelay(true).
			withParsingOpts(parsingOpts...).
			build(t)
		tablelandClient := setup.newTablelandClient(t)

		ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
		tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts
		caller := txOpts.From

		_, err := sc.CreateTable(txOpts, caller, `CREATE TABLE foo_1337 (bar text);`)
		require.NoError(t, err)
		backend.Commit()

		ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller.Hex())
		_, err = tbld.RelayWriteQuery(ctx, tableland.RelayWriteQueryRequest{
			Statement: "INSERT INTO foo_1337_1 (bar) VALUES ('hello')", // length of 45 bytes
		})
		require.NoError(t, err)
	})

	t.Run("write-query-size-nok", func(t *testing.T) {
		t.Parallel()

		parsingOpts := []parsing.Option{
			parsing.WithMaxWriteQuerySize(45),
		}
		setup := newTablelandSetupBuilder().
			withAllowTransactionRelay(true).
			withParsingOpts(parsingOpts...).
			build(t)
		tablelandClient := setup.newTablelandClient(t)

		ctx := setup.ctx
		tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts
		caller := txOpts.From

		ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller.Hex())
		_, err := tbld.RelayWriteQuery(ctx, tableland.RelayWriteQueryRequest{
			Statement: "INSERT INTO foo_1337_1 (bar) VALUES ('hello2')", // length of 46 bytes
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "write query size is too long")
	})

	t.Run("read-query-size-ok", func(t *testing.T) {
		t.Parallel()

		parsingOpts := []parsing.Option{
			parsing.WithMaxReadQuerySize(44),
		}

		setup := newTablelandSetupBuilder().
			withAllowTransactionRelay(true).
			withParsingOpts(parsingOpts...).
			build(t)
		tablelandClient := setup.newTablelandClient(t)

		ctx, backend, sc := setup.ctx, setup.ethClient, setup.contract
		tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts
		caller := txOpts.From

		_, err := sc.CreateTable(txOpts, caller, `CREATE TABLE foo_1337 (bar text);`)
		require.NoError(t, err)
		backend.Commit()

		ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller.Hex())
		require.Eventually(t,
			func() bool {
				_, err := tbld.RunReadQuery(ctx, tableland.RunReadQueryRequest{
					Statement: "SELECT * FROM foo_1337_1 WHERE bar = 'hello'", // length of 44 bytes
				})
				return err == nil
			},
			5*time.Second,
			100*time.Millisecond,
		)
	})

	t.Run("read-query-size-nok", func(t *testing.T) {
		t.Parallel()

		parsingOpts := []parsing.Option{
			parsing.WithMaxReadQuerySize(44),
		}

		setup := newTablelandSetupBuilder().
			withAllowTransactionRelay(true).
			withParsingOpts(parsingOpts...).
			build(t)
		tablelandClient := setup.newTablelandClient(t)

		ctx := setup.ctx
		tbld, txOpts := tablelandClient.tableland, tablelandClient.txOpts
		caller := txOpts.From

		ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller.Hex())
		_, err := tbld.RunReadQuery(ctx, tableland.RunReadQueryRequest{
			Statement: "SELECT * FROM foo_1337_1 WHERE bar = 'hello2'", // length of 45 bytes
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "read query size is too long")
	})
}

func TestAllowTransactionRelayConfig(t *testing.T) {
	t.Parallel()

	setup := newTablelandSetupBuilder().
		withAllowTransactionRelay(false).
		build(t)

	tablelandClient := setup.newTablelandClient(t)

	ctx, tbld, txOpts := setup.ctx, tablelandClient.tableland, tablelandClient.txOpts

	t.Run("relay write query", func(t *testing.T) {
		_, err := relayWriteQuery(ctx, t, tbld, "INSERT INTO foo_1337_1 VALUES ('bar', 0)", txOpts.From.Hex())
		require.Error(t, err)
		require.ErrorContains(t, err, "chain id 1337 does not suppport relaying of transactions")
	})

	t.Run("set controller", func(t *testing.T) {
		_, err := setController(ctx, t, tbld, txOpts.From.Hex(), "", "1") // values don't matter
		require.Error(t, err)
		require.ErrorContains(t, err, "chain id 1337 does not suppport relaying of transactions")
	})
}

func processCSV(
	ctx context.Context,
	t *testing.T,
	caller common.Address,
	tbld tableland.Tableland,
	csvPath string,
	backend *backends.SimulatedBackend,
) {
	t.Helper()

	ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller.Hex())
	records := readCsvFile(t, csvPath)
	for _, record := range records {
		if record[0] == "r" {
			req := tableland.RunReadQueryRequest{Statement: record[1]}
			require.Eventually(t, jsonEq(ctx, t, tbld, req, record[2]), time.Second*5, time.Millisecond*100)
		} else {
			req := tableland.RelayWriteQueryRequest{Statement: record[1]}
			_, err := tbld.RelayWriteQuery(ctx, req)
			require.NoError(t, err)
			backend.Commit()
		}
	}
}

func jsonEq(
	ctx context.Context,
	t *testing.T,
	tbld tableland.Tableland,
	req tableland.RunReadQueryRequest,
	expJSON string,
) func() bool {
	return func() bool {
		r, err := tbld.RunReadQuery(ctx, req)
		// if we get a table undefined error, try again
		if err != nil && strings.Contains(err.Error(), "no such table") {
			return false
		}
		require.NoError(t, err)

		b, err := json.Marshal(r.Result)
		require.NoError(t, err)

		gotJSON := string(b)

		var o1 interface{}
		var o2 interface{}

		err = json.Unmarshal([]byte(expJSON), &o1)
		if err != nil {
			return false
		}
		err = json.Unmarshal([]byte(gotJSON), &o2)
		if err != nil {
			return false
		}

		return reflect.DeepEqual(o1, o2)
	}
}

func runSQLCountEq(
	ctx context.Context,
	t *testing.T,
	tbld tableland.Tableland,
	sql string,
	address string,
	expCount int,
) func() bool {
	return func() bool {
		response, err := runReadQuery(ctx, t, tbld, sql, address)
		// if we get a table undefined error, try again
		if err != nil && strings.Contains(err.Error(), "table not found") {
			return false
		}
		require.NoError(t, err)

		responseInBytes, err := json.Marshal(response)
		require.NoError(t, err)

		r := &struct {
			Data struct {
				Rows [][]interface{} `json:"rows"`
			} `json:"data"`
		}{}

		err = json.Unmarshal(responseInBytes, r)
		require.NoError(t, err)

		return len(r.Data.Rows) == expCount
	}
}

func runReadQuery(
	ctx context.Context,
	t *testing.T,
	tbld tableland.Tableland,
	sql string,
	controller string,
) (tableland.RunReadQueryResponse, error) {
	t.Helper()

	ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, controller)
	req := tableland.RunReadQueryRequest{
		Statement: sql,
	}

	return tbld.RunReadQuery(ctx, req)
}

func relayWriteQuery(
	ctx context.Context,
	t *testing.T,
	tbld tableland.Tableland,
	sql string,
	caller string,
) (tableland.RelayWriteQueryResponse, error) {
	t.Helper()

	ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller)
	req := tableland.RelayWriteQueryRequest{
		Statement: sql,
	}

	return tbld.RelayWriteQuery(ctx, req)
}

func setController(
	ctx context.Context,
	t *testing.T,
	tbld tableland.Tableland,
	caller string,
	controller string,
	tokenID string,
) (tableland.SetControllerResponse, error) {
	t.Helper()

	ctx = context.WithValue(ctx, middlewares.ContextKeyAddress, caller)
	req := tableland.SetControllerRequest{
		Controller: controller,
		TokenID:    tokenID,
	}

	return tbld.SetController(ctx, req)
}

func readCsvFile(t *testing.T, filePath string) [][]string {
	t.Helper()

	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("unable to read input file "+filePath, err)
	}
	defer f.Close() // nolint

	csvReader := csv.NewReader(f)
	records, err := csvReader.ReadAll()
	if err != nil {
		t.Fatalf("unable to parse file as CSV for "+filePath, err)
	}

	return records
}

type aclHalfMock struct {
	sqlStore sqlstore.SystemStore
}

func (acl *aclHalfMock) CheckPrivileges(
	ctx context.Context,
	tx *sql.Tx,
	controller common.Address,
	id tableland.TableID,
	op tableland.Operation,
) (bool, error) {
	aclImpl := NewACL(acl.sqlStore, nil)
	return aclImpl.CheckPrivileges(ctx, tx, controller, id, op)
}

func (acl *aclHalfMock) IsOwner(ctx context.Context, controller common.Address, id tableland.TableID) (bool, error) {
	return true, nil
}

func requireReceipts(ctx context.Context, t *testing.T, tbld tableland.Tableland, txnHashes []string, ok bool) {
	t.Helper()

	for _, txnHash := range txnHashes {
		r, err := tbld.GetReceipt(ctx, tableland.GetReceiptRequest{
			TxnHash: txnHash,
		})
		require.NoError(t, err)
		require.True(t, r.Ok)
		require.NotNil(t, r.Receipt)
		require.Equal(t, tableland.ChainID(1337), r.Receipt.ChainID)
		require.Equal(t, txnHash, txnHash)
		require.NotZero(t, r.Receipt.BlockNumber)
		if ok {
			require.Nil(t, r.Receipt.Error)
			require.NotNil(t, r.Receipt.TableID)
			require.NotZero(t, r.Receipt.TableID)
		} else {
			require.NotNil(t, r.Receipt.Error)
			require.NotEmpty(t, *r.Receipt.Error)
			require.Nil(t, r.Receipt.TableID)
		}
	}
}

func requireTxn(
	t *testing.T,
	backend *backends.SimulatedBackend,
	key *ecdsa.PrivateKey,
	from common.Address,
	to common.Address,
	amt *big.Int,
) {
	nonce, err := backend.PendingNonceAt(context.Background(), from)
	require.NoError(t, err)

	gasLimit := uint64(21000)
	gasPrice, err := backend.SuggestGasPrice(context.Background())
	require.NoError(t, err)

	var data []byte
	txnData := &types.LegacyTx{
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      gasLimit,
		To:       &to,
		Data:     data,
		Value:    amt,
	}
	tx := types.NewTx(txnData)
	signedTx, err := types.SignTx(tx, types.HomesteadSigner{}, key)
	require.NoError(t, err)

	bal, err := backend.BalanceAt(context.Background(), from, nil)
	require.NoError(t, err)
	require.NotZero(t, bal)

	err = backend.SendTransaction(context.Background(), signedTx)
	require.NoError(t, err)

	backend.Commit()

	receipt, err := backend.TransactionReceipt(context.Background(), signedTx.Hash())
	require.NoError(t, err)
	require.NotNil(t, receipt)
}

type tablelandSetupBuilder struct {
	allowTransactionRelay bool
	parsingOpts           []parsing.Option
}

func newTablelandSetupBuilder() *tablelandSetupBuilder {
	return &tablelandSetupBuilder{}
}

func (b *tablelandSetupBuilder) withAllowTransactionRelay(v bool) *tablelandSetupBuilder {
	b.allowTransactionRelay = v
	return b
}

func (b *tablelandSetupBuilder) withParsingOpts(opts ...parsing.Option) *tablelandSetupBuilder {
	b.parsingOpts = opts
	return b
}

func (b *tablelandSetupBuilder) build(t *testing.T) *tablelandSetup {
	t.Helper()
	dbURI := tests.Sqlite3URI()

	ctx := context.WithValue(context.Background(), middlewares.ContextKeyChainID, tableland.ChainID(1337))
	store, err := system.New(dbURI, tableland.ChainID(1337))
	require.NoError(t, err)

	parser, err := parserimpl.New([]string{"system_", "registry", "sqlite_"}, b.parsingOpts...)
	require.NoError(t, err)

	ex, err := executor.NewExecutor(1337, dbURI, parser, 0, &aclHalfMock{store})
	require.NoError(t, err)

	backend, addr, sc, auth, sk := testutil.Setup(t)

	// Spin up dependencies needed for the EventProcessor.
	// i.e: Executor, Parser, and EventFeed (connected to the EVM chain)
	ef, err := efimpl.New(1337, backend, addr, eventfeed.WithMinBlockDepth(0))
	require.NoError(t, err)

	// Create EventProcessor for our test.
	ep, err := epimpl.New(parser, ex, ef, 1337)
	require.NoError(t, err)

	err = ep.Start()
	require.NoError(t, err)
	t.Cleanup(func() { ep.Stop() })

	userStore, err := user.New(dbURI)
	require.NoError(t, err)

	return &tablelandSetup{
		ctx: ctx,

		// ethereum client
		ethClient: backend,

		// contract bindings
		contract:     sc,
		contractAddr: addr,

		// contract deployer
		deployerPrivateKey: sk,
		deployerTxOpts:     auth,

		// common dependencies among mesa clients
		parser:      parser,
		userStore:   userStore,
		systemStore: store,

		// configs
		allowTransactionRelay: b.allowTransactionRelay,
	}
}

type tablelandSetup struct {
	ctx context.Context

	// ethereum client
	ethClient *backends.SimulatedBackend

	// contract bindings
	contract     *ethereum.Contract
	contractAddr common.Address

	// contract deployer
	deployerPrivateKey *ecdsa.PrivateKey
	deployerTxOpts     *bind.TransactOpts

	// common dependencies among tableland clients
	parser      parsing.SQLValidator
	userStore   *user.UserStore
	systemStore *system.SystemStore

	// configs
	allowTransactionRelay bool
}

func (s *tablelandSetup) newTablelandClient(t *testing.T) *tablelandClient {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	wallet, err := wallet.NewWallet(hex.EncodeToString(crypto.FromECDSA(key)))
	require.NoError(t, err)

	txOpts, err := bind.NewKeyedTransactorWithChainID(key, big.NewInt(1337)) // nolint
	require.NoError(t, err)

	requireTxn(t,
		s.ethClient,
		s.deployerPrivateKey,
		s.deployerTxOpts.From,
		wallet.Address(),
		big.NewInt(1000000000000000000),
	)

	registry, err := ethereum.NewClient(
		s.ethClient,
		1337,
		s.contractAddr,
		wallet,
		impl.NewSimpleTracker(wallet, s.ethClient),
	)
	require.NoError(t, err)
	tbld := NewTablelandMesa(
		s.parser,
		s.userStore,
		map[tableland.ChainID]chains.ChainStack{
			1337: {
				Store:                 s.systemStore,
				Registry:              registry,
				AllowTransactionRelay: s.allowTransactionRelay,
			},
		})

	return &tablelandClient{
		tableland: tbld,
		txOpts:    txOpts,
	}
}

type tablelandClient struct {
	tableland tableland.Tableland
	txOpts    *bind.TransactOpts
}
