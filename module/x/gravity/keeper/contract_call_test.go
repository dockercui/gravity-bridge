package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/peggyjv/gravity-bridge/module/v5/x/gravity/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContractCallTxExecuted(t *testing.T) {
	input := CreateTestEnv(t)
	ctx := input.Context.WithBlockHeight(100)
	storeKey := input.GravityStoreKey
	cdc := input.Marshaler

	latestEthereumBlockHeight := &types.LatestEthereumBlockHeight{
		CosmosHeight:   100,
		EthereumHeight: 1000,
	}

	ctx.KVStore(storeKey).Set([]byte{types.LastEthereumBlockHeightKey}, cdc.MustMarshal(latestEthereumBlockHeight))

	scope := []byte("test-scope")
	contract := common.HexToAddress("0x2a24af0501a534fca004ee1bd667b783f205a546")
	nonce1 := uint64(1)
	nonce2 := uint64(2)
	payload := []byte("payload")
	erc20Tokens := []types.ERC20Token{
		{
			Contract: "0x2a24af0501a534fca004ee1bd667b783f205a546",
			Amount:   sdk.NewInt(1),
		},
	}

	input.GravityKeeper.CreateContractCallTx(
		ctx,
		nonce1,
		scope,
		contract,
		payload,
		erc20Tokens,
		erc20Tokens,
	)

	input.GravityKeeper.CreateContractCallTx(
		ctx,
		nonce2,
		scope,
		contract,
		payload,
		erc20Tokens,
		erc20Tokens,
	)

	cctx1 := input.GravityKeeper.GetOutgoingTx(ctx, types.MakeContractCallTxKey(scope, nonce1)).(*types.ContractCallTx)
	assert.Equal(t, cctx1.InvalidationScope, scope)
	assert.Equal(t, cctx1.InvalidationNonce, nonce1)
	assert.Equal(t, cctx1.Address, contract.Hex())
	assert.Equal(t, cctx1.Payload, payload)
	assert.Equal(t, cctx1.Tokens, erc20Tokens)
	assert.Equal(t, cctx1.Fees, erc20Tokens)

	cctx2 := input.GravityKeeper.GetOutgoingTx(ctx, types.MakeContractCallTxKey(scope, nonce2)).(*types.ContractCallTx)
	assert.Equal(t, cctx2.InvalidationScope, scope)
	assert.Equal(t, cctx2.InvalidationNonce, nonce2)
	assert.Equal(t, cctx2.Address, contract.Hex())
	assert.Equal(t, cctx2.Payload, payload)
	assert.Equal(t, cctx2.Tokens, erc20Tokens)
	assert.Equal(t, cctx2.Fees, erc20Tokens)

	input.GravityKeeper.contractCallExecuted(ctx, scope, nonce2)

	otx1 := input.GravityKeeper.GetOutgoingTx(ctx, types.MakeContractCallTxKey(scope, nonce1))
	otx2 := input.GravityKeeper.GetOutgoingTx(ctx, types.MakeContractCallTxKey(scope, nonce2))

	assert.Nil(t, otx1)
	assert.Nil(t, otx2)
}

func TestGetUnconfirmedContractCallTxs(t *testing.T) {
	input, ctx := SetupFiveValChain(t)
	gk := input.GravityKeeper
	vals := input.StakingKeeper.GetAllValidators(ctx)
	val1, err := sdk.ValAddressFromBech32(vals[0].OperatorAddress)
	require.NoError(t, err)
	val2, err := sdk.ValAddressFromBech32(vals[1].OperatorAddress)
	require.NoError(t, err)

	scope := []byte("test")
	address := common.HexToAddress("0x2a24af0501a534fca004ee1bd667b783f205a546")
	payload := []byte("payload")
	tokens := []types.ERC20Token{}
	fees := []types.ERC20Token{}
	sig := []byte("dummysig")
	gk.CreateContractCallTx(ctx, 1, scope, address, payload, tokens, fees)
	gk.SetCompletedOutgoingTx(ctx, &types.ContractCallTx{
		InvalidationNonce: 2,
		InvalidationScope: scope,
		Address:           address.Hex(),
		Payload:           payload,
		Tokens:            tokens,
		Fees:              fees,
		Height:            uint64(ctx.BlockHeight()),
	})

	// val1 signs both
	// val2 signs one
	gk.SetEthereumSignature(
		ctx,
		&types.ContractCallTxConfirmation{
			InvalidationNonce: 1,
			InvalidationScope: scope,
			Signature:         sig,
		},
		val1,
	)
	gk.SetEthereumSignature(
		ctx,
		&types.ContractCallTxConfirmation{
			InvalidationNonce: 2,
			InvalidationScope: scope,
			Signature:         sig,
		},
		val1,
	)
	gk.SetEthereumSignature(
		ctx,
		&types.ContractCallTxConfirmation{
			InvalidationNonce: 2,
			InvalidationScope: scope,
			Signature:         sig,
		},
		val2,
	)

	require.Empty(t, gk.GetUnsignedContractCallTxs(ctx, val1))
	require.Equal(t, 1, len(gk.GetUnsignedContractCallTxs(ctx, val2)))
}

func TestOrderContractCallsByNonceAscending(t *testing.T) {
	t.Run("normal case", func(t *testing.T) {
		// Create test contract calls with different nonces
		calls := []*types.ContractCallTx{
			{InvalidationNonce: 3},
			{InvalidationNonce: 1},
			{InvalidationNonce: 4},
			{InvalidationNonce: 2},
		}

		// Order the contract calls
		orderedCalls := orderContractCallsByNonceAscending(calls)

		// Check if the contract calls are ordered correctly
		assert.Equal(t, uint64(1), orderedCalls[0].InvalidationNonce)
		assert.Equal(t, uint64(2), orderedCalls[1].InvalidationNonce)
		assert.Equal(t, uint64(3), orderedCalls[2].InvalidationNonce)
		assert.Equal(t, uint64(4), orderedCalls[3].InvalidationNonce)

		// Check if the length of the slice remains the same
		assert.Equal(t, len(calls), len(orderedCalls))
	})

	t.Run("empty slice", func(t *testing.T) {
		calls := []*types.ContractCallTx{}
		orderedCalls := orderContractCallsByNonceAscending(calls)
		assert.Empty(t, orderedCalls)
	})

	t.Run("nil slice", func(t *testing.T) {
		var calls []*types.ContractCallTx
		orderedCalls := orderContractCallsByNonceAscending(calls)
		assert.Nil(t, orderedCalls)
	})

	t.Run("single element", func(t *testing.T) {
		calls := []*types.ContractCallTx{{InvalidationNonce: 1}}
		orderedCalls := orderContractCallsByNonceAscending(calls)
		assert.Equal(t, 1, len(orderedCalls))
		assert.Equal(t, uint64(1), orderedCalls[0].InvalidationNonce)
	})

	t.Run("duplicate nonces", func(t *testing.T) {
		calls := []*types.ContractCallTx{
			{InvalidationNonce: 2},
			{InvalidationNonce: 1},
			{InvalidationNonce: 2},
			{InvalidationNonce: 1},
		}
		orderedCalls := orderContractCallsByNonceAscending(calls)
		assert.Equal(t, 4, len(orderedCalls))
		assert.Equal(t, uint64(1), orderedCalls[0].InvalidationNonce)
		assert.Equal(t, uint64(1), orderedCalls[1].InvalidationNonce)
		assert.Equal(t, uint64(2), orderedCalls[2].InvalidationNonce)
		assert.Equal(t, uint64(2), orderedCalls[3].InvalidationNonce)
	})
}
