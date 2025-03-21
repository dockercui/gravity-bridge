package keeper

import (
	"math/big"

	"cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/ethereum/go-ethereum/common"

	"github.com/peggyjv/gravity-bridge/module/v6/x/gravity/types"
)

func (k Keeper) DetectMaliciousSupply(ctx sdk.Context, denom string, amount sdk.Int) (err error) {
	currentSupply := k.bankKeeper.GetSupply(ctx, denom)
	newSupply := new(big.Int).Add(currentSupply.Amount.BigInt(), amount.BigInt())
	if newSupply.BitLen() > 256 {
		return errors.Wrapf(types.ErrSupplyOverflow, "malicious supply of %s detected", denom)
	}

	return nil
}

// Handle is the entry point for EthereumEvent processing
func (k Keeper) Handle(ctx sdk.Context, eve types.EthereumEvent) (err error) {
	switch event := eve.(type) {
	case *types.SendToCosmosEvent:
		// Check if coin is Cosmos-originated asset and get denom
		isCosmosOriginated, denom := k.ERC20ToDenomLookup(ctx, common.HexToAddress(event.TokenContract))
		addr, _ := sdk.AccAddressFromBech32(event.CosmosReceiver)
		coins := sdk.Coins{sdk.NewCoin(denom, event.Amount)}

		if !isCosmosOriginated {
			if err := k.DetectMaliciousSupply(ctx, denom, event.Amount); err != nil {
				return err
			}

			// if it is not cosmos originated, mint the coins (aka vouchers)
			if err := k.bankKeeper.MintCoins(ctx, types.ModuleName, coins); err != nil {
				return errors.Wrapf(err, "mint vouchers coins: %s", coins)
			}
		}

		if recipientModule, ok := k.ReceiverModuleAccounts[event.CosmosReceiver]; ok {
			if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, recipientModule, coins); err != nil {
				return err
			}
		} else {
			if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, addr, coins); err != nil {
				return err
			}
		}
		k.AfterSendToCosmosEvent(ctx, *event)
		return nil

	case *types.BatchExecutedEvent:
		k.batchTxExecuted(ctx, common.HexToAddress(event.TokenContract), event.BatchNonce)
		k.AfterBatchExecutedEvent(ctx, *event)
		return nil

	case *types.ERC20DeployedEvent:
		if err := k.verifyERC20DeployedEvent(ctx, event); err != nil {
			return err
		}

		// add to denom-erc20 mapping
		k.setCosmosOriginatedDenomToERC20(ctx, event.CosmosDenom, common.HexToAddress(event.TokenContract))
		k.AfterERC20DeployedEvent(ctx, *event)
		return nil

	case *types.ContractCallExecutedEvent:
		k.contractCallExecuted(ctx, event.InvalidationScope.Bytes(), event.InvalidationNonce)
		k.AfterContractCallExecutedEvent(ctx, *event)
		return nil

	case *types.SignerSetTxExecutedEvent:
		k.SignerSetExecuted(ctx, event.GetEventNonce())
		k.AfterSignerSetExecutedEvent(ctx, *event)
		return nil

	default:
		return errors.Wrapf(types.ErrInvalid, "event type: %T", event)
	}
}

func (k Keeper) verifyERC20DeployedEvent(ctx sdk.Context, event *types.ERC20DeployedEvent) error {
	if existingERC20, exists := k.getCosmosOriginatedERC20(ctx, event.CosmosDenom); exists {
		return errors.Wrapf(
			types.ErrInvalidERC20Event,
			"ERC20 token %s already exists for denom %s", existingERC20.Hex(), event.CosmosDenom,
		)
	}

	// We expect that all Cosmos-based tokens have metadata defined. In the case
	// a token does not have metadata defined, e.g. an IBC token, we successfully
	// handle the token under the following conditions:
	//
	// 1. The ERC20 name is equal to the token's denomination. Otherwise, this
	// 		means that ERC20 tokens would have an untenable UX.
	// 2. The ERC20 token has zero decimals as this is what we default to since
	// 		we cannot know or infer the real decimal value for the Cosmos token.
	// 3. The ERC20 symbol is empty.
	//
	// NOTE: This path is not encouraged and all supported assets should have
	// metadata defined. If metadata cannot be defined, consider adding the token's
	// metadata on the fly.
	if md, ok := k.bankKeeper.GetDenomMetaData(ctx, event.CosmosDenom); ok && md.Base != "" {
		return verifyERC20Token(md, event)
	}

	if supply := k.bankKeeper.GetSupply(ctx, event.CosmosDenom); supply.IsZero() {
		return errors.Wrapf(
			types.ErrInvalidERC20Event,
			"no supply exists for token %s without metadata", event.CosmosDenom,
		)
	}

	if event.Erc20Name != event.CosmosDenom {
		return errors.Wrapf(
			types.ErrInvalidERC20Event,
			"invalid ERC20 name for token without metadata; got: %s, expected: %s", event.Erc20Name, event.CosmosDenom,
		)
	}

	if event.Erc20Symbol != "" {
		return errors.Wrapf(
			types.ErrInvalidERC20Event,
			"expected empty ERC20 symbol for token without metadata; got: %s", event.Erc20Symbol,
		)
	}

	if event.Erc20Decimals != 0 {
		return errors.Wrapf(
			types.ErrInvalidERC20Event,
			"expected zero ERC20 decimals for token without metadata; got: %d", event.Erc20Decimals,
		)
	}

	return nil
}

func verifyERC20Token(metadata banktypes.Metadata, event *types.ERC20DeployedEvent) error {
	if event.Erc20Name != metadata.Display {
		return errors.Wrapf(
			types.ErrInvalidERC20Event,
			"ERC20 name %s does not match the denom display %s", event.Erc20Name, metadata.Display,
		)
	}

	if event.Erc20Symbol != metadata.Display {
		return errors.Wrapf(
			types.ErrInvalidERC20Event,
			"ERC20 symbol %s does not match denom display %s", event.Erc20Symbol, metadata.Display,
		)
	}

	// ERC20 tokens use a very simple mechanism to tell you where to display the
	// decimal point. The "decimals" field simply tells you how many decimal places
	// there will be.
	//
	// Cosmos denoms have a system that is much more full featured, with
	// enterprise-ready token denominations. There is a DenomUnits array that
	// tells you what the name of each denomination of the token is.
	//
	// To correlate this with an ERC20 "decimals" field, we have to search through
	// the DenomUnits array to find the DenomUnit which matches up to the main
	// token "display" value. Then we take the "exponent" from this DenomUnit.
	//
	// If the correct DenomUnit is not found, it will default to 0. This will
	// result in there being no decimal places in the token's ERC20 on Ethereum.
	// For example, if this happened with ATOM, 1 ATOM would appear on Ethereum
	// as 1 million ATOM, having 6 extra places before the decimal point.
	var decimals uint32
	for _, denomUnit := range metadata.DenomUnits {
		if denomUnit.Denom == metadata.Display {
			decimals = denomUnit.Exponent
			break
		}
	}

	if uint64(decimals) != event.Erc20Decimals {
		return errors.Wrapf(
			types.ErrInvalidERC20Event,
			"ERC20 decimals %d does not match denom decimals %d", event.Erc20Decimals, decimals,
		)
	}

	return nil
}
