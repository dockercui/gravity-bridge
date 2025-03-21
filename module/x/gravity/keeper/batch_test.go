package keeper

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/peggyjv/gravity-bridge/module/v6/x/gravity/types"
)

func TestBatches(t *testing.T) {
	input := CreateTestEnv(t)
	ctx := input.Context
	var (
		now                 = time.Now().UTC()
		mySender, _         = sdk.AccAddressFromBech32("cosmos1ahx7f8wyertuus9r20284ej0asrs085case3kn")
		myReceiver          = common.HexToAddress("0xd041c41EA1bf0F006ADBb6d2c9ef9D425dE5eaD7")
		myTokenContractAddr = common.HexToAddress("0x429881672B9AE42b8EbA0E26cD9C73711b891Ca5") // Pickle
		allVouchers         = sdk.NewCoins(
			types.NewERC20Token(99999, myTokenContractAddr).GravityCoin(),
		)
	)

	// mint some voucher first
	require.NoError(t, input.BankKeeper.MintCoins(ctx, types.ModuleName, allVouchers))
	// set senders balance
	input.AccountKeeper.NewAccountWithAddress(ctx, mySender)
	require.NoError(t, fundAccount(ctx, input.BankKeeper, mySender, allVouchers))

	// CREATE FIRST BATCH
	// ==================

	// add some TX to the pool
	input.AddSendToEthTxsToPool(t, ctx, myTokenContractAddr, mySender, myReceiver, 2, 3, 2, 1)

	// when
	ctx = ctx.WithBlockTime(now)

	// tx batch size is 2, so that some of them stay behind
	firstBatch := input.GravityKeeper.CreateBatchTx(ctx, myTokenContractAddr, 2)

	// then batch is persisted
	gotFirstBatch := input.GravityKeeper.GetOutgoingTx(ctx, firstBatch.GetStoreIndex())
	require.NotNil(t, gotFirstBatch)

	gfb := gotFirstBatch.(*types.BatchTx)
	expFirstBatch := &types.BatchTx{
		BatchNonce: 1,
		Transactions: []*types.SendToEthereum{
			types.NewSendToEthereumTx(2, myTokenContractAddr, mySender, myReceiver, 101, 3),
			types.NewSendToEthereumTx(3, myTokenContractAddr, mySender, myReceiver, 102, 2),
		},
		TokenContract: myTokenContractAddr.Hex(),
		Height:        1234567,
	}

	assert.Equal(t, expFirstBatch.Transactions, gfb.Transactions)

	// and verify remaining available Tx in the pool
	var gotUnbatchedTx []*types.SendToEthereum
	input.GravityKeeper.IterateUnbatchedSendToEthereums(ctx, func(tx *types.SendToEthereum) bool {
		gotUnbatchedTx = append(gotUnbatchedTx, tx)
		return false
	})
	expUnbatchedTx := []*types.SendToEthereum{
		types.NewSendToEthereumTx(1, myTokenContractAddr, mySender, myReceiver, 100, 2),
		types.NewSendToEthereumTx(4, myTokenContractAddr, mySender, myReceiver, 103, 1),
	}
	assert.Equal(t, expUnbatchedTx, gotUnbatchedTx)

	// CREATE SECOND, MORE PROFITABLE BATCH
	// ====================================

	// add some more TX to the pool to create a more profitable batch
	input.AddSendToEthTxsToPool(t, ctx, myTokenContractAddr, mySender, myReceiver, 4, 5)

	// create the more profitable batch
	ctx = ctx.WithBlockTime(now)
	// tx batch size is 2, so that some of them stay behind
	secondBatch := input.GravityKeeper.CreateBatchTx(ctx, myTokenContractAddr, 2)

	// check that the more profitable batch has the right txs in it
	expSecondBatch := &types.BatchTx{
		BatchNonce: 2,
		Transactions: []*types.SendToEthereum{
			types.NewSendToEthereumTx(6, myTokenContractAddr, mySender, myReceiver, 101, 5),
			types.NewSendToEthereumTx(5, myTokenContractAddr, mySender, myReceiver, 100, 4),
		},
		TokenContract: myTokenContractAddr.Hex(),
		Height:        1234567,
	}

	assert.Equal(t, expSecondBatch, secondBatch)

	// EXECUTE THE MORE PROFITABLE BATCH
	// =================================

	// Execute the batch
	input.GravityKeeper.batchTxExecuted(ctx, common.HexToAddress(secondBatch.TokenContract), secondBatch.BatchNonce)

	// check batch has been deleted
	gotSecondBatch := input.GravityKeeper.GetOutgoingTx(ctx, secondBatch.GetStoreIndex())
	require.Nil(t, gotSecondBatch)

	// check that txs from first batch have been freed
	gotUnbatchedTx = nil
	input.GravityKeeper.IterateUnbatchedSendToEthereums(ctx, func(tx *types.SendToEthereum) bool {
		gotUnbatchedTx = append(gotUnbatchedTx, tx)
		return false
	})
	expUnbatchedTx = []*types.SendToEthereum{
		types.NewSendToEthereumTx(2, myTokenContractAddr, mySender, myReceiver, 101, 3),
		types.NewSendToEthereumTx(3, myTokenContractAddr, mySender, myReceiver, 102, 2),
		types.NewSendToEthereumTx(1, myTokenContractAddr, mySender, myReceiver, 100, 2),
		types.NewSendToEthereumTx(4, myTokenContractAddr, mySender, myReceiver, 103, 1),
	}
	assert.Equal(t, expUnbatchedTx, gotUnbatchedTx)
}

// tests that batches work with large token amounts, mostly a duplicate of the above
// tests but using much bigger numbers
func TestBatchesFullCoins(t *testing.T) {
	input := CreateTestEnv(t)
	ctx := input.Context
	var (
		now                 = time.Now().UTC()
		mySender, _         = sdk.AccAddressFromBech32("cosmos1ahx7f8wyertuus9r20284ej0asrs085case3kn")
		myReceiver          = common.HexToAddress("0xd041c41EA1bf0F006ADBb6d2c9ef9D425dE5eaD7")
		myTokenContractAddr = common.HexToAddress("0x429881672B9AE42b8EbA0E26cD9C73711b891Ca5")
		totalCoins, _       = sdk.NewIntFromString("150000000000000000000000") // 150,000 ETH worth
		oneEth, _           = sdk.NewIntFromString("1000000000000000000")
		allVouchers         = sdk.NewCoins(
			types.NewSDKIntERC20Token(totalCoins, myTokenContractAddr).GravityCoin(),
		)
	)

	// mint some voucher first
	require.NoError(t, input.BankKeeper.MintCoins(ctx, types.ModuleName, allVouchers))
	// set senders balance
	input.AccountKeeper.NewAccountWithAddress(ctx, mySender)
	require.NoError(t, fundAccount(ctx, input.BankKeeper, mySender, allVouchers))

	// Create different transactions with large amounts and unique fees
	for _, v := range []uint64{200, 300, 250, 100} {
		vAsSDKInt := sdk.NewIntFromUint64(v)
		amount := types.NewSDKIntERC20Token(oneEth.Mul(vAsSDKInt), myTokenContractAddr).GravityCoin()
		fee := types.NewSDKIntERC20Token(oneEth.Mul(vAsSDKInt), myTokenContractAddr).GravityCoin()
		_, err := input.GravityKeeper.createSendToEthereum(ctx, mySender, myReceiver.Hex(), amount, fee)
		require.NoError(t, err)
	}

	// Check that the first batch gets created with max 2 transactions
	ctx = ctx.WithBlockTime(now)
	firstBatch := input.GravityKeeper.CreateBatchTx(ctx, myTokenContractAddr, 2)
	require.NotNil(t, firstBatch)
	require.Equal(t, 2, len(firstBatch.Transactions))
	require.Equal(t, oneEth.Mul(sdk.NewIntFromUint64(300)), firstBatch.Transactions[0].Erc20Fee.Amount)
	require.Equal(t, oneEth.Mul(sdk.NewIntFromUint64(250)), firstBatch.Transactions[1].Erc20Fee.Amount)

	// Add a new transaction with even higher fee
	vAsSDKInt := sdk.NewIntFromUint64(400)
	amount := types.NewSDKIntERC20Token(oneEth.Mul(vAsSDKInt), myTokenContractAddr).GravityCoin()
	fee := types.NewSDKIntERC20Token(oneEth.Mul(vAsSDKInt), myTokenContractAddr).GravityCoin()
	_, err := input.GravityKeeper.createSendToEthereum(ctx, mySender, myReceiver.Hex(), amount, fee)
	require.NoError(t, err)

	// Create second batch - should contain at most 2 transactions
	// Remember, we don't cancel the older batch in CreateBatchTx because it could be in flight by the time we create the second.
	// If a newer batch gets executed first, the SendToEthereums in the older batch will be freed up.
	ctx = ctx.WithBlockTime(now)
	secondBatch := input.GravityKeeper.CreateBatchTx(ctx, myTokenContractAddr, 2)
	require.NotNil(t, secondBatch)
	require.Equal(t, 2, len(secondBatch.Transactions))
	require.Equal(t, oneEth.Mul(sdk.NewIntFromUint64(400)), secondBatch.Transactions[0].Erc20Fee.Amount)
	require.Equal(t, oneEth.Mul(sdk.NewIntFromUint64(200)), secondBatch.Transactions[1].Erc20Fee.Amount)

	// Verify the remaining unbatched transaction
	var gotUnbatchedTx []*types.SendToEthereum
	input.GravityKeeper.IterateUnbatchedSendToEthereums(ctx, func(tx *types.SendToEthereum) bool {
		gotUnbatchedTx = append(gotUnbatchedTx, tx)
		return false
	})
	require.Equal(t, 1, len(gotUnbatchedTx))
	require.Equal(t, oneEth.Mul(sdk.NewIntFromUint64(100)), gotUnbatchedTx[0].Erc20Fee.Amount)
}

func TestPoolTxRefund(t *testing.T) {
	input := CreateTestEnv(t)
	ctx := input.Context
	var (
		now                 = time.Now().UTC()
		mySender, _         = sdk.AccAddressFromBech32("cosmos1ahx7f8wyertuus9r20284ej0asrs085case3kn")
		myReceiver          = common.HexToAddress("0xd041c41EA1bf0F006ADBb6d2c9ef9D425dE5eaD7")
		myTokenContractAddr = common.HexToAddress("0x429881672B9AE42b8EbA0E26cD9C73711b891Ca5") // Pickle
		allVouchers         = sdk.NewCoins(
			types.NewERC20Token(414, myTokenContractAddr).GravityCoin(),
		)
		myDenom = types.NewERC20Token(1, myTokenContractAddr).GravityCoin().Denom
	)

	// mint some voucher first
	require.NoError(t, input.BankKeeper.MintCoins(ctx, types.ModuleName, allVouchers))
	// set senders balance
	input.AccountKeeper.NewAccountWithAddress(ctx, mySender)
	require.NoError(t, fundAccount(ctx, input.BankKeeper, mySender, allVouchers))

	// CREATE FIRST BATCH
	// ==================

	// add some TX to the pool
	// for i, v := range []uint64{2, 3, 2, 1} {
	// 	amount := types.NewERC20Token(uint64(i+100), myTokenContractAddr).GravityCoin()
	// 	fee := types.NewERC20Token(v, myTokenContractAddr).GravityCoin()
	// 	_, err := input.GravityKeeper.CreateSendToEthereum(ctx, mySender, myReceiver, amount, fee)
	// 	require.NoError(t, err)
	// }
	input.AddSendToEthTxsToPool(t, ctx, myTokenContractAddr, mySender, myReceiver, 2, 3, 2, 1)

	// when
	ctx = ctx.WithBlockTime(now)

	// tx batch size is 2, so that some of them stay behind
	input.GravityKeeper.CreateBatchTx(ctx, myTokenContractAddr, 2)

	// try to refund a tx that's in a batch
	err := input.GravityKeeper.cancelSendToEthereum(ctx, 2, mySender.String())
	require.Error(t, err)

	// try to refund a tx that's in the pool
	err = input.GravityKeeper.cancelSendToEthereum(ctx, 4, mySender.String())
	require.NoError(t, err)

	// make sure refund was issued
	balances := input.BankKeeper.GetAllBalances(ctx, mySender)
	require.Equal(t, sdk.NewInt(104), balances.AmountOf(myDenom))
}

func TestEmptyBatch(t *testing.T) {
	input := CreateTestEnv(t)
	ctx := input.Context

	var (
		now                 = time.Now().UTC()
		mySender, _         = sdk.AccAddressFromBech32("cosmos1ahx7f8wyertuus9r20284ej0asrs085case3kn")
		myTokenContractAddr = common.HexToAddress("0x429881672B9AE42b8EbA0E26cD9C73711b891Ca5") // Pickle
		allVouchers         = sdk.NewCoins(
			types.NewERC20Token(99999, myTokenContractAddr).GravityCoin(),
		)
	)

	// mint some voucher first
	require.NoError(t, input.BankKeeper.MintCoins(ctx, types.ModuleName, allVouchers))
	// set senders balance
	input.AccountKeeper.NewAccountWithAddress(ctx, mySender)
	require.NoError(t, fundAccount(ctx, input.BankKeeper, mySender, allVouchers))

	// no transactions should be included in this batch
	ctx = ctx.WithBlockTime(now)
	batchTx := input.GravityKeeper.CreateBatchTx(ctx, myTokenContractAddr, 2)

	require.Nil(t, batchTx)
}

func TestGetUnconfirmedBatchTxs(t *testing.T) {
	input, ctx := SetupFiveValChain(t)
	gk := input.GravityKeeper
	vals := input.StakingKeeper.GetAllValidators(ctx)
	val1, err := sdk.ValAddressFromBech32(vals[0].OperatorAddress)
	require.NoError(t, err)
	val2, err := sdk.ValAddressFromBech32(vals[1].OperatorAddress)
	require.NoError(t, err)

	blockheight := uint64(ctx.BlockHeight())
	sig := []byte("dummysig")
	gk.SetCompletedOutgoingTx(ctx, &types.BatchTx{
		BatchNonce: 1,
		Height:     uint64(ctx.BlockHeight()),
	})
	gk.SetOutgoingTx(ctx, &types.BatchTx{
		BatchNonce: 2,
		Height:     uint64(ctx.BlockHeight()),
	})

	// val1 signs both
	// val2 signs one
	gk.SetEthereumSignature(
		ctx,
		&types.BatchTxConfirmation{
			BatchNonce: 1,
			Signature:  sig,
		},
		val1,
	)
	gk.SetEthereumSignature(
		ctx,
		&types.BatchTxConfirmation{
			BatchNonce: 2,
			Signature:  sig,
		},
		val1,
	)
	gk.SetEthereumSignature(
		ctx,
		&types.BatchTxConfirmation{
			BatchNonce: 1,
			Signature:  sig,
		},
		val2,
	)

	require.Empty(t, gk.GetUnsignedBatchTxs(ctx, val1))
	require.Equal(t, 1, len(gk.GetUnsignedBatchTxs(ctx, val2)))

	// Confirm ordering
	val3, err := sdk.ValAddressFromBech32(vals[2].OperatorAddress)
	require.NoError(t, err)

	addressA := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	addressB := "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	gk.SetCompletedOutgoingTx(ctx, &types.BatchTx{
		TokenContract: addressB,
		BatchNonce:    3,
		Height:        blockheight,
	})
	gk.SetCompletedOutgoingTx(ctx, &types.BatchTx{
		TokenContract: addressA,
		BatchNonce:    4,
		Height:        blockheight,
	})
	gk.SetOutgoingTx(ctx, &types.BatchTx{
		TokenContract: addressB,
		BatchNonce:    5,
		Height:        blockheight,
	})
	gk.SetOutgoingTx(ctx, &types.BatchTx{
		TokenContract: addressA,
		BatchNonce:    6,
		Height:        blockheight,
	})
	gk.SetOutgoingTx(ctx, &types.BatchTx{
		TokenContract: addressB,
		BatchNonce:    7,
		Height:        blockheight,
	})

	unconfirmed := gk.GetUnsignedBatchTxs(ctx, val3)

	require.EqualValues(t, 7, len(unconfirmed))
	require.EqualValues(t, unconfirmed[0].BatchNonce, 1)
	require.EqualValues(t, unconfirmed[1].BatchNonce, 2)
	require.EqualValues(t, unconfirmed[2].BatchNonce, 3)
	require.EqualValues(t, unconfirmed[3].BatchNonce, 4)
	require.EqualValues(t, unconfirmed[4].BatchNonce, 5)
	require.EqualValues(t, unconfirmed[5].BatchNonce, 6)
	require.EqualValues(t, unconfirmed[6].BatchNonce, 7)
}

func TestCancelBatchTx(t *testing.T) {
	input := CreateTestEnv(t)
	ctx := input.Context
	var (
		now                 = time.Now().UTC()
		mySender, _         = sdk.AccAddressFromBech32("cosmos1ahx7f8wyertuus9r20284ej0asrs085case3kn")
		myReceiver          = common.HexToAddress("0xd041c41EA1bf0F006ADBb6d2c9ef9D425dE5eaD7")
		myTokenContractAddr = common.HexToAddress("0x429881672B9AE42b8EbA0E26cD9C73711b891Ca5") // Pickle
		allVouchers         = sdk.NewCoins(
			types.NewERC20Token(99999, myTokenContractAddr).GravityCoin(),
		)
	)

	// mint some voucher first
	require.NoError(t, input.BankKeeper.MintCoins(ctx, types.ModuleName, allVouchers))
	// set senders balance
	input.AccountKeeper.NewAccountWithAddress(ctx, mySender)
	require.NoError(t, fundAccount(ctx, input.BankKeeper, mySender, allVouchers))

	// add some TX to the pool
	input.AddSendToEthTxsToPool(t, ctx, myTokenContractAddr, mySender, myReceiver, 2, 3, 2, 1)

	// when
	ctx = ctx.WithBlockTime(now)

	// tx batch size is 2, so that some of them stay behind
	firstBatch := input.GravityKeeper.CreateBatchTx(ctx, myTokenContractAddr, 2)

	// ensure the batch was created
	require.NotNil(t, firstBatch)
	require.Equal(t, uint64(1), firstBatch.BatchNonce)
	require.Len(t, firstBatch.Transactions, 2)

	// verify the batch exists in the store
	gotBatch := input.GravityKeeper.GetOutgoingTx(ctx, firstBatch.GetStoreIndex())
	require.NotNil(t, gotBatch)

	// cancel the batch
	input.GravityKeeper.CancelBatchTx(ctx, firstBatch)

	// verify the batch no longer exists in the store
	gotBatch = input.GravityKeeper.GetOutgoingTx(ctx, firstBatch.GetStoreIndex())
	require.Nil(t, gotBatch)

	// verify that the transactions are back in the pool
	var gotUnbatchedTx []*types.SendToEthereum
	input.GravityKeeper.IterateUnbatchedSendToEthereums(ctx, func(tx *types.SendToEthereum) bool {
		gotUnbatchedTx = append(gotUnbatchedTx, tx)
		return false
	})
	require.Len(t, gotUnbatchedTx, 4) // All 4 original transactions should be back in the pool

	// Create a new batch for testing partial signing
	secondBatch := input.GravityKeeper.CreateBatchTx(ctx, myTokenContractAddr, 2)
	require.NotNil(t, secondBatch)

	// Add a partial signature to the batch
	val1 := sdk.ValAddress([]byte("validator1"))
	input.GravityKeeper.SetEthereumSignature(ctx, &types.BatchTxConfirmation{
		TokenContract: secondBatch.TokenContract,
		BatchNonce:    secondBatch.BatchNonce,
		Signature:     []byte("partial_sig"),
	}, val1)

	// Cancel the partially signed batch
	input.GravityKeeper.CancelBatchTx(ctx, secondBatch)

	// Verify the batch is removed and transactions are back in the pool
	gotBatch = input.GravityKeeper.GetOutgoingTx(ctx, secondBatch.GetStoreIndex())
	require.Nil(t, gotBatch)

	gotUnbatchedTx = []*types.SendToEthereum{}
	input.GravityKeeper.IterateUnbatchedSendToEthereums(ctx, func(tx *types.SendToEthereum) bool {
		gotUnbatchedTx = append(gotUnbatchedTx, tx)
		return false
	})
	require.Len(t, gotUnbatchedTx, 4) // All 4 transactions should be back in the pool
}

func TestOrderBatchesByNonceAscending(t *testing.T) {
	t.Run("normal case", func(t *testing.T) {
		// Create test batches with different nonces
		batches := []*types.BatchTx{
			{BatchNonce: 3},
			{BatchNonce: 1},
			{BatchNonce: 4},
			{BatchNonce: 2},
		}

		// Order the batches
		orderedBatches := orderBatchesByNonceAscending(batches)

		// Check if the batches are ordered correctly
		assert.Equal(t, uint64(1), orderedBatches[0].BatchNonce)
		assert.Equal(t, uint64(2), orderedBatches[1].BatchNonce)
		assert.Equal(t, uint64(3), orderedBatches[2].BatchNonce)
		assert.Equal(t, uint64(4), orderedBatches[3].BatchNonce)

		// Check if the length of the slice remains the same
		assert.Equal(t, len(batches), len(orderedBatches))
	})

	t.Run("empty slice", func(t *testing.T) {
		batches := []*types.BatchTx{}
		orderedBatches := orderBatchesByNonceAscending(batches)
		assert.Empty(t, orderedBatches)
	})

	t.Run("nil slice", func(t *testing.T) {
		var batches []*types.BatchTx
		orderedBatches := orderBatchesByNonceAscending(batches)
		assert.Nil(t, orderedBatches)
	})

	t.Run("single element", func(t *testing.T) {
		batches := []*types.BatchTx{{BatchNonce: 1}}
		orderedBatches := orderBatchesByNonceAscending(batches)
		assert.Equal(t, 1, len(orderedBatches))
		assert.Equal(t, uint64(1), orderedBatches[0].BatchNonce)
	})

	t.Run("duplicate nonces", func(t *testing.T) {
		batches := []*types.BatchTx{
			{BatchNonce: 2},
			{BatchNonce: 1},
			{BatchNonce: 2},
			{BatchNonce: 1},
		}
		orderedBatches := orderBatchesByNonceAscending(batches)
		assert.Equal(t, 4, len(orderedBatches))
		assert.Equal(t, uint64(1), orderedBatches[0].BatchNonce)
		assert.Equal(t, uint64(1), orderedBatches[1].BatchNonce)
		assert.Equal(t, uint64(2), orderedBatches[2].BatchNonce)
		assert.Equal(t, uint64(2), orderedBatches[3].BatchNonce)
	})
}
