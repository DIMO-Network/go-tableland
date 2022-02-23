package impl

import (
	"context"
	"time"

	"github.com/textileio/go-tableland/internal/tableland"
	"github.com/textileio/go-tableland/pkg/parsing"
	"github.com/textileio/go-tableland/pkg/sqlstore"
)

// ThrottledSQLStorePGX implements a throttled SQLStore interface using pgx.
type ThrottledSQLStorePGX struct {
	store sqlstore.SQLStore
	delay time.Duration
}

// NewThrottledSQLStorePGX creates a new pgx pool and instantiate both the user and system stores.
func NewThrottledSQLStorePGX(store sqlstore.SQLStore, delay time.Duration) sqlstore.SQLStore {
	return &ThrottledSQLStorePGX{store, delay}
}

// GetTable fetchs a table from its UUID.
func (s *ThrottledSQLStorePGX) GetTable(ctx context.Context, id tableland.TableID) (sqlstore.Table, error) {
	return s.store.GetTable(ctx, id)
}

// GetTablesByController fetchs a table from controller address.
func (s *ThrottledSQLStorePGX) GetTablesByController(ctx context.Context,
	controller string) ([]sqlstore.Table, error) {
	return s.store.GetTablesByController(ctx, controller)
}

// Authorize grants the provided address permission to use the system.
func (s *ThrottledSQLStorePGX) Authorize(ctx context.Context, address string) error {
	return s.store.Authorize(ctx, address)
}

// Revoke removes permission to use the system from the provided address.
func (s *ThrottledSQLStorePGX) Revoke(ctx context.Context, address string) error {
	return s.store.Revoke(ctx, address)
}

// IsAuthorized checks if the provided address has permission to use the system.
func (s *ThrottledSQLStorePGX) IsAuthorized(
	ctx context.Context,
	address string,
) (sqlstore.IsAuthorizedResult, error) {
	return s.store.IsAuthorized(ctx, address)
}

// GetAuthorizationRecord gets the authorization record for the provided address.
func (s *ThrottledSQLStorePGX) GetAuthorizationRecord(
	ctx context.Context,
	address string,
) (sqlstore.AuthorizationRecord, error) {
	return s.store.GetAuthorizationRecord(ctx, address)
}

// ListAuthorized returns a list of all authorization records.
func (s *ThrottledSQLStorePGX) ListAuthorized(ctx context.Context) ([]sqlstore.AuthorizationRecord, error) {
	return s.store.ListAuthorized(ctx)
}

// IncrementCreateTableCount increments the counter.
func (s *ThrottledSQLStorePGX) IncrementCreateTableCount(ctx context.Context, address string) error {
	return s.store.IncrementCreateTableCount(ctx, address)
}

// IncrementRunSQLCount increments the counter.
func (s *ThrottledSQLStorePGX) IncrementRunSQLCount(ctx context.Context, address string) error {
	return s.store.IncrementRunSQLCount(ctx, address)
}

// Read executes a read statement on the db.
func (s *ThrottledSQLStorePGX) Read(ctx context.Context, stmt parsing.SugaredReadStmt) (interface{}, error) {
	data, err := s.store.Read(ctx, stmt)
	time.Sleep(s.delay)

	return data, err
}

// Close closes the connection pool.
func (s *ThrottledSQLStorePGX) Close() {
	s.store.Close()
}