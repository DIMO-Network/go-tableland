package impl

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/abi/bind/backends"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	noncepkg "github.com/textileio/go-tableland/pkg/nonce"
	sqlstoreimpl "github.com/textileio/go-tableland/pkg/sqlstore/impl"
	"github.com/textileio/go-tableland/pkg/tableregistry/impl/ethereum"
	"github.com/textileio/go-tableland/pkg/tableregistry/impl/testutil"
	"github.com/textileio/go-tableland/pkg/wallet"
	"github.com/textileio/go-tableland/tests"
)

func TestTracker(t *testing.T) {
	ctx := context.Background()
	tracker, backend, contract, txOpts, wallet := setup(ctx, t)

	fn1, unlock1, nonce1 := tracker.GetNonce(ctx)
	txn1, err := contract.RunSQL(txOpts, "tbl", wallet.Address(), "INSERT ...")
	require.NoError(t, err)
	backend.Commit()
	fn1(txn1.Hash())
	unlock1()

	fn2, unlock2, nonce2 := tracker.GetNonce(ctx)
	txn2, err := contract.RunSQL(txOpts, "tbl", wallet.Address(), "INSERT ...")
	require.NoError(t, err)
	backend.Commit()
	fn2(txn2.Hash())
	unlock2()

	fn3, unlock3, nonce3 := tracker.GetNonce(ctx)
	txn3, err := contract.RunSQL(txOpts, "tbl", wallet.Address(), "INSERT ...")
	require.NoError(t, err)
	backend.Commit()
	fn3(txn3.Hash())
	unlock3()

	require.Equal(t, int64(0), nonce1)
	require.Equal(t, int64(1), nonce2)
	require.Equal(t, int64(2), nonce3)
	require.Eventually(t, func() bool {
		return tracker.GetPendingCount(ctx) == 0
	}, 5*time.Second, time.Second)
}

func TestTrackerUnlock(t *testing.T) {
	ctx := context.Background()
	tracker, backend, contract, txOpts, wallet := setup(ctx, t)

	_, unlock, nonce1 := tracker.GetNonce(ctx)
	// this go routine simulates a concurrent runSQL call that went wrong
	go func() {
		time.Sleep(1 * time.Second)
		unlock()
	}()

	// this call will be blocked until nonce tracker is unblocked
	fn2, unlock2, nonce2 := tracker.GetNonce(ctx)
	txn2, err := contract.RunSQL(txOpts, "tbl", wallet.Address(), "INSERT ...")
	require.NoError(t, err)
	backend.Commit()
	fn2(txn2.Hash())
	unlock2()

	require.Equal(t, int64(0), nonce1)
	require.Equal(t, int64(0), nonce2) // nonce2 should not have been incremented
	require.Eventually(t, func() bool {
		return tracker.GetPendingCount(ctx) == 0
	}, 5*time.Second, time.Second)
}

func TestTrackerPendingTxGotStuck(t *testing.T) {
	ctx := context.Background()
	tracker, backend, contract, txOpts, wallet := setup(ctx, t)

	fn1, unlock1, nonce1 := tracker.GetNonce(ctx)
	txn1, err := contract.RunSQL(txOpts, "tbl", wallet.Address(), "INSERT ...")
	require.NoError(t, err)
	backend.Commit()
	fn1(txn1.Hash())
	unlock1()

	fn2, unlock2, nonce2 := tracker.GetNonce(ctx)
	txn2, err := contract.RunSQL(txOpts, "tbl", wallet.Address(), "INSERT ...")
	require.NoError(t, err)
	//backend.Commit() , this tx will get stuck
	fn2(txn2.Hash())
	unlock2()

	require.Equal(t, int64(0), nonce1)
	require.Equal(t, int64(1), nonce2)
	require.Eventually(t, func() bool {
		return tracker.GetPendingCount(ctx) == 1
	}, 5*time.Second, time.Second)
}

func setup(ctx context.Context, t *testing.T) (
	noncepkg.NonceTracker,
	*backends.SimulatedBackend,
	*ethereum.Contract,
	*bind.TransactOpts,
	*wallet.Wallet) {
	url := tests.PostgresURL(t)

	backend, _, contract, txOpts, _ := testutil.Setup(t)

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	wallet, err := wallet.NewWallet(hex.EncodeToString(crypto.FromECDSA(key)))
	require.NoError(t, err)

	sqlstore, err := sqlstoreimpl.New(ctx, url)
	require.NoError(t, err)

	tracker, err := NewLocalTracker(
		ctx,
		wallet,
		&NonceStore{sqlstore},
		backend,
		500*time.Millisecond,
		0,
		10*time.Minute)
	require.NoError(t, err)

	return tracker, backend, contract, txOpts, wallet
}

func TestInitialization(t *testing.T) {
	ctx := context.Background()
	url := tests.PostgresURL(t)

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	wallet, err := wallet.NewWallet(hex.EncodeToString(crypto.FromECDSA(key)))
	require.NoError(t, err)

	sqlstore, err := sqlstoreimpl.New(ctx, url)
	require.NoError(t, err)

	// Tests the case of a non-fresh wallet that has already submitted some txs
	// and we are not aware of (simulated by PendingNonceAt() returning 10).
	{
		tracker := &LocalTracker{
			wallet:      wallet,
			network:     noncepkg.EthereumNetwork,
			nonceStore:  &NonceStore{sqlstore},
			chainClient: &ChainMock{},
		}

		err = tracker.initialize(ctx)
		require.NoError(t, err)

		nonce, err := tracker.nonceStore.GetNonce(ctx, noncepkg.EthereumNetwork, wallet.Address())
		require.NoError(t, err)

		require.Equal(t, int64(10), nonce.Nonce)
		require.Equal(t, 0, tracker.GetPendingCount(ctx))
	}

	// Tests the case of a non-fresh wallet that has already submitted some txs
	// and we are aware of (simulated by GetNonce() returning 10) and has some pending tx.
	{
		testAddress := wallet.Address()

		// insert two pending txs (nonce 0 and nonce 1)
		nonceStore := &NonceStore{sqlstore}
		err := nonceStore.InsertPendingTxAndUpsertNonce(
			ctx,
			noncepkg.EthereumNetwork,
			testAddress,
			0,
			common.HexToHash("0x119f50bf7f1ff2daa4712119af9dbd429ab727690565f93193f63650b020bc30"),
		)
		require.NoError(t, err)

		err = nonceStore.InsertPendingTxAndUpsertNonce(
			ctx,
			noncepkg.EthereumNetwork,
			testAddress,
			1,
			common.HexToHash("0x7a0edee97ea3543c279a7329665cc851a9ea53a39ad5bbce55338052808a23a9"),
		)
		require.NoError(t, err)

		tracker := &LocalTracker{
			wallet:      wallet,
			network:     noncepkg.EthereumNetwork,
			nonceStore:  &NonceStore{sqlstore},
			chainClient: nil,
		}

		err = tracker.initialize(ctx)
		require.NoError(t, err)

		nonce, err := tracker.nonceStore.GetNonce(ctx, noncepkg.EthereumNetwork, testAddress)
		require.NoError(t, err)

		require.Equal(t, int64(1), nonce.Nonce)
		require.Equal(t, 2, tracker.GetPendingCount(ctx))
	}
}

func TestMinBlockDepth(t *testing.T) {
	ctx := context.Background()
	url := tests.PostgresURL(t)

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	wallet, err := wallet.NewWallet(hex.EncodeToString(crypto.FromECDSA(key)))
	require.NoError(t, err)

	sqlstore, err := sqlstoreimpl.New(ctx, url)
	require.NoError(t, err)

	testAddress := wallet.Address()

	// insert two pending txs (nonce 0 and nonce 1)
	nonceStore := &NonceStore{sqlstore}
	err = nonceStore.InsertPendingTxAndUpsertNonce(
		ctx,
		noncepkg.EthereumNetwork,
		testAddress,
		0,
		common.HexToHash("0x119f50bf7f1ff2daa4712119af9dbd429ab727690565f93193f63650b020bc30"),
	)
	require.NoError(t, err)

	err = nonceStore.InsertPendingTxAndUpsertNonce(
		ctx,
		noncepkg.EthereumNetwork,
		testAddress,
		1,
		common.HexToHash("0x7a0edee97ea3543c279a7329665cc851a9ea53a39ad5bbce55338052808a23a9"),
	)
	require.NoError(t, err)

	tracker := &LocalTracker{
		wallet:      wallet,
		network:     noncepkg.EthereumNetwork,
		nonceStore:  &NonceStore{sqlstore},
		chainClient: &ChainMock{},

		pendingTxs: []noncepkg.PendingTx{{
			Nonce:     0,
			Hash:      common.HexToHash("0x119f50bf7f1ff2daa4712119af9dbd429ab727690565f93193f63650b020bc30"),
			Network:   noncepkg.EthereumNetwork,
			Address:   testAddress,
			CreatedAt: time.Now(),
		}, {
			Nonce:     1,
			Hash:      common.HexToHash("0x7a0edee97ea3543c279a7329665cc851a9ea53a39ad5bbce55338052808a23a9"),
			Network:   noncepkg.EthereumNetwork,
			Address:   testAddress,
			CreatedAt: time.Now(),
		}},

		minBlockChainDepth: 5,
	}

	err = tracker.initialize(ctx)
	require.NoError(t, err)

	// For the first pending Tx, the head number is 10 and block number 1
	// The pending tx will be considered confirmed, and it will be deleted
	h := &types.Header{Number: big.NewInt(10)}
	err = tracker.checkIfPendingTxWasIncluded(ctx, tracker.pendingTxs[0], h)
	require.NoError(t, err)
	require.Equal(t, 1, tracker.GetPendingCount(ctx))
	txs, err := nonceStore.ListPendingTx(ctx, noncepkg.EthereumNetwork, testAddress)
	require.NoError(t, err)
	require.Equal(t, 1, len(txs))

	// For the second pending Tx, the head number is 10 and block number 6
	// The pending tx will not be considered confirmed, and it will not be deleted
	err = tracker.checkIfPendingTxWasIncluded(ctx, tracker.pendingTxs[0], h)
	require.Equal(t, noncepkg.ErrBlockDiffNotEnough, err)
	require.Equal(t, 1, tracker.GetPendingCount(ctx))
	txs, err = nonceStore.ListPendingTx(ctx, noncepkg.EthereumNetwork, testAddress)
	require.NoError(t, err)
	require.Equal(t, 1, len(txs))

	// We advance the head to 11
	h = &types.Header{Number: big.NewInt(11)}
	err = tracker.checkIfPendingTxWasIncluded(ctx, tracker.pendingTxs[0], h)
	require.NoError(t, err)
	require.Equal(t, 0, tracker.GetPendingCount(ctx))
	txs, err = nonceStore.ListPendingTx(ctx, noncepkg.EthereumNetwork, testAddress)
	require.NoError(t, err)
	require.Equal(t, 0, len(txs))
}

func TestCheckIfPendingTxIsStuck(t *testing.T) {
	ctx := context.Background()
	url := tests.PostgresURL(t)

	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	wallet, err := wallet.NewWallet(hex.EncodeToString(crypto.FromECDSA(key)))
	require.NoError(t, err)

	sqlstore, err := sqlstoreimpl.New(ctx, url)
	require.NoError(t, err)

	testAddress := wallet.Address()

	// insert two pending txs (nonce 0 and nonce 1)
	nonceStore := &NonceStore{sqlstore}
	err = nonceStore.InsertPendingTxAndUpsertNonce(
		ctx,
		noncepkg.EthereumNetwork,
		testAddress,
		0,
		common.HexToHash("0xda3601329d295f03dc75bf42569f476f22995c456334c9a39a05e7cb7877dc41"),
	)
	require.NoError(t, err)

	tracker := &LocalTracker{
		wallet:      wallet,
		network:     noncepkg.EthereumNetwork,
		nonceStore:  &NonceStore{sqlstore},
		chainClient: &ChainMock{},

		pendingTxs: []noncepkg.PendingTx{{
			Nonce:     0,
			Hash:      common.HexToHash("0xda3601329d295f03dc75bf42569f476f22995c456334c9a39a05e7cb7877dc41"),
			Network:   noncepkg.EthereumNetwork,
			Address:   testAddress,
			CreatedAt: time.Now(),
		}},

		minBlockChainDepth: 5,
		// very small duration just to catch the ErrPendingTxMayBeStuck error
		stuckInterval: time.Duration(1000),
	}

	err = tracker.initialize(ctx)
	require.NoError(t, err)

	h := &types.Header{Number: big.NewInt(10)}
	err = tracker.checkIfPendingTxWasIncluded(ctx, tracker.pendingTxs[0], h)
	require.Equal(t, noncepkg.ErrPendingTxMayBeStuck, err)
	require.Equal(t, 1, tracker.GetPendingCount(ctx))
	txs, err := nonceStore.ListPendingTx(ctx, noncepkg.EthereumNetwork, testAddress)
	require.NoError(t, err)
	require.Equal(t, 1, len(txs))
}

type ChainMock struct{}

// Using this for TestInitialization.
func (m *ChainMock) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return 10, nil
}

// Using this for test TestMinBlockDepth and TestCheckIfPendingTxIsStuck.
func (m *ChainMock) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	if txHash.Hex() == "0x119f50bf7f1ff2daa4712119af9dbd429ab727690565f93193f63650b020bc30" {
		r := &types.Receipt{BlockNumber: big.NewInt(1)}
		return r, nil
	}

	if txHash.Hex() == "0x7a0edee97ea3543c279a7329665cc851a9ea53a39ad5bbce55338052808a23a9" {
		r := &types.Receipt{BlockNumber: big.NewInt(6)}
		return r, nil
	}

	// this is used for TestCheckIfPendingTxIsStuck
	return nil, errors.New("not found")
}

// this is not used by any test.
func (m *ChainMock) HeaderByNumber(ctx context.Context, n *big.Int) (*types.Header, error) {
	return nil, nil
}