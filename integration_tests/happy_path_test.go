package integration_tests

import (
	"context"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	govtypesv1beta1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/peggyjv/gravity-bridge/module/v4/x/gravity/types"
)

func (s *IntegrationTestSuite) TestHappyPath() {
	s.Run("Bring up chain, and test the happy path", func() {
		s.T().Logf("approving Gravity to spend ERC 20")
		err := s.approveERC20()
		s.Require().NoError(err, "error approving spending balance for the gravity contract on behalf of the first validator")

		allowance, err := s.getERC20AllowanceOf(common.HexToAddress(s.chain.validators[0].ethereumKey.address), gravityContract)
		s.Require().NoError(err, "error getting allowance of gravity contract spending on behalf of first validator")
		s.Require().Equal(UInt256Max(), allowance.BigInt(), "spending allowance not set correctly, got: %s", allowance.String())

		initialBalance, err := s.getEthTokenBalanceOf(common.HexToAddress(s.chain.validators[0].ethereumKey.address), testERC20contract)
		s.Require().NoError(err, "error getting first validator balance")
		s.Require().Equal(sdk.NewUint(10000).BigInt(), initialBalance.BigInt(), "balance was %s, expected 10000", initialBalance.String())

		for _, val := range s.chain.validators {
			kb, err := val.keyring()
			s.Require().NoError(err)
			clientCtx, err := s.chain.clientContext("tcp://localhost:26657", &kb, "val", val.address())
			s.Require().NoError(err)

			queryClient := banktypes.NewQueryClient(clientCtx)
			res, err := queryClient.AllBalances(context.Background(),
				&banktypes.QueryAllBalancesRequest{
					Address: s.chain.validators[1].address().String(),
				})
			s.Require().NoError(err)
			s.T().Logf("balances for %s: %s", val.address().String(), res.Balances)
		}

		// send from val 0 on eth to val 1 on cosmos
		s.T().Logf("sending to cosmos")
		err = s.sendToCosmos(s.chain.validators[1].address(), sdk.NewInt(200))
		s.Require().NoError(err, "error sending test denom to cosmos")

		for _, val := range s.chain.validators {
			kb, err := val.keyring()
			s.Require().NoError(err)
			clientCtx, err := s.chain.clientContext("tcp://localhost:26657", &kb, "val", val.address())
			s.Require().NoError(err)

			queryClient := banktypes.NewQueryClient(clientCtx)
			res, err := queryClient.AllBalances(context.Background(),
				&banktypes.QueryAllBalancesRequest{
					Address: s.chain.validators[1].address().String(),
				})
			s.Require().NoError(err)
			s.T().Logf("balances for %s: %s", val.address().String(), res.Balances)
		}

		var gravityDenom string
		s.Require().Eventuallyf(func() bool {
			val := s.chain.validators[0]
			kb, err := val.keyring()
			s.Require().NoError(err)
			clientCtx, err := s.chain.clientContext("tcp://localhost:26657", &kb, "val", val.address())
			s.Require().NoError(err)

			bankQueryClient := banktypes.NewQueryClient(clientCtx)
			res, err := bankQueryClient.AllBalances(context.Background(),
				&banktypes.QueryAllBalancesRequest{
					Address: s.chain.validators[1].address().String(),
				})
			if err != nil {
				return false
			}

			gbQueryClient := types.NewQueryClient(clientCtx)
			denomRes, err := gbQueryClient.ERC20ToDenom(context.Background(),
				&types.ERC20ToDenomRequest{
					Erc20: testERC20contract.String(),
				})
			if err != nil {
				s.T().Logf("error querying ERC20 denom %s, %e", testERC20contract.String(), err)
				return false
			}
			s.Require().False(denomRes.CosmosOriginated, "ERC20-originated denom marked as cosmos originated")
			gravityDenom = denomRes.Denom

			for _, coin := range res.Balances {
				if coin.Denom == gravityDenom && coin.Amount.Equal(sdk.NewInt(200)) {
					return true
				}
			}

			s.T().Logf("balance not found, received %v", res.Balances)

			return false
		}, 105*time.Second, 10*time.Second, "balance never found on cosmos")

		s.T().Logf("sending to ethereum")
		sendToEthereumMsg := types.NewMsgSendToEthereum(
			s.chain.validators[1].address(),
			s.chain.validators[1].ethereumKey.address,
			sdk.Coin{Denom: gravityDenom, Amount: sdk.NewInt(100)},
			sdk.Coin{Denom: gravityDenom, Amount: sdk.NewInt(1)},
		)

		s.Require().Eventuallyf(func() bool {
			val := s.chain.validators[1]
			keyring, err := val.keyring()
			s.Require().NoError(err)
			clientCtx, err := s.chain.clientContext("tcp://localhost:26657", &keyring, "val", val.address())
			s.Require().NoError(err)

			response, err := s.chain.sendMsgs(*clientCtx, sendToEthereumMsg)
			if err != nil {
				s.T().Logf("error: %s", err)
				return false
			}
			if response.Code != 0 {
				if response.Code != 32 {
					s.T().Log(response)
				}

				return false
			}
			return true
		}, 105*time.Second, 10*time.Second, "unable to send to ethereum")

		s.T().Logf("funding community pool")
		orch := s.chain.orchestrators[0]
		clientCtx, err := s.chain.clientContext("tcp://localhost:26657", orch.keyring, "orch", orch.address())
		s.Require().NoError(err)

		fundCommunityPoolMsg := distrtypes.NewMsgFundCommunityPool(
			sdk.NewCoins(sdk.NewCoin(testDenom, sdk.NewInt(1000000000))),
			orch.address(),
		)

		s.Require().Eventuallyf(func() bool {
			response, err := s.chain.sendMsgs(*clientCtx, fundCommunityPoolMsg)
			if err != nil {
				s.T().Logf("error: %s", err)
				return false
			}
			if response.Code != 0 {
				if response.Code != 32 {
					s.T().Log(response)
				}
				return false
			}
			return true
		}, 105*time.Second, 10*time.Second, "unable to fund community pool")

		s.Require().Eventuallyf(func() bool {
			distrQueryClient := distrtypes.NewQueryClient(clientCtx)
			poolRes, err := distrQueryClient.CommunityPool(context.Background(),
				&distrtypes.QueryCommunityPoolRequest{},
			)
			s.Require().NoError(err, "error retrieving community pool")
			s.Require().Greater(poolRes.Pool.AmountOf(testDenom).BigInt().Int64(), sdk.NewDec(1000000000).BigInt().Int64())

			return true
		}, 20*time.Second, 2*time.Second, "community pool balance not high enough")

		s.T().Logf("deploying testgb as an ERC20")
		gbQueryClient := types.NewQueryClient(clientCtx)
		paramsRes, err := gbQueryClient.DenomToERC20Params(context.Background(),
			&types.DenomToERC20ParamsRequest{
				Denom: testDenom,
			})
		s.Require().NoError(err, "error retrieving ERC20 params for testgb denom")

		err = s.deployERC20(paramsRes.BaseDenom, paramsRes.Erc20Name, paramsRes.Erc20Symbol, uint8(paramsRes.Erc20Decimals))
		s.Require().NoError(err, "error deploying testgb as an ERC20")

		s.Require().Eventuallyf(func() bool {
			erc20Res, err := gbQueryClient.DenomToERC20(context.Background(),
				&types.DenomToERC20Request{
					Denom: testDenom,
				},
			)
			if err != nil {
				s.T().Logf("erc20 not deployed yet, waiting")
				return false
			}

			s.Require().True(erc20Res.CosmosOriginated)
			return true
		}, 180*time.Second, 10*time.Second, "unable to verify ERC20 deployment")

		erc20Res, err := gbQueryClient.DenomToERC20(context.Background(),
			&types.DenomToERC20Request{
				Denom: testDenom,
			},
		)
		s.Require().NoError(err, "error querying ERC20 for testgb denom")
		erc20Contract := common.HexToAddress(erc20Res.Erc20)
		initialBalance, err = s.getEthTokenBalanceOf(common.HexToAddress(s.chain.validators[2].ethereumKey.address), erc20Contract)
		s.Require().NoError(err, "error getting destination balance")

		s.T().Logf("create governance proposal to fund an ethereum address")
		orch = s.chain.orchestrators[0]
		clientCtx, err = s.chain.clientContext("tcp://localhost:26657", orch.keyring, "orch", orch.address())
		s.Require().NoError(err)

		sendAmount := int64(900)
		proposal := types.CommunityPoolEthereumSpendProposal{
			Title:       "community pool spend ethereum",
			Description: "community pool spend ethereum",
			Recipient:   s.chain.validators[2].ethereumKey.address,
			Amount:      sdk.NewCoin(testDenom, sdk.NewInt(sendAmount)),
			BridgeFee:   sdk.NewCoin(testDenom, sdk.NewInt(1000000)),
		}

		proposalMsg, err := govtypesv1beta1.NewMsgSubmitProposal(
			&proposal,
			sdk.Coins{
				{
					Denom:  testDenom,
					Amount: sdk.NewInt(2),
				},
			},
			orch.address(),
		)
		s.Require().NoError(err, "unable to create governance proposal")

		s.T().Log("submit proposal spending community pool funds")
		submitProposalResponse, err := s.chain.sendMsgs(*clientCtx, proposalMsg)
		s.Require().NoError(err)
		s.Require().Zero(submitProposalResponse.Code, "raw log: %s", submitProposalResponse.RawLog)

		govQueryClient := govtypesv1beta1.NewQueryClient(clientCtx)

		s.Require().Eventually(func() bool {
			s.T().Log("check proposal was submitted correctly")
			govQueryClient = govtypesv1beta1.NewQueryClient(clientCtx)
			proposalsQueryResponse, err := govQueryClient.Proposals(context.Background(), &govtypesv1beta1.QueryProposalsRequest{})
			s.Require().NoError(err)
			s.Require().NotEmpty(proposalsQueryResponse.Proposals)
			s.Require().Equal(uint64(1), proposalsQueryResponse.Proposals[0].ProposalId, "not proposal id 1")
			s.Require().Equal(govtypesv1beta1.StatusVotingPeriod, proposalsQueryResponse.Proposals[0].Status, "proposal not in voting period")
			return true
		}, 20*time.Second, 2*time.Second, "proposal not submitted correctly")

		s.T().Log("vote for community spend proposal")
		for _, val := range s.chain.validators {
			kr, err := val.keyring()
			s.Require().NoError(err)
			clientCtx, err := s.chain.clientContext("tcp://localhost:26657", &kr, "val", val.address())
			s.Require().NoError(err)

			voteMsg := govtypesv1beta1.NewMsgVote(val.address(), 1, govtypesv1beta1.OptionYes)
			voteResponse, err := s.chain.sendMsgs(*clientCtx, voteMsg)
			s.Require().NoError(err)
			s.Require().Zero(voteResponse.Code, "vote error: %s", voteResponse.RawLog)
		}

		s.T().Log("wait for community spend proposal to be approved")
		s.Require().Eventuallyf(func() bool {
			proposalQueryResponse, err := govQueryClient.Proposal(context.Background(), &govtypesv1beta1.QueryProposalRequest{ProposalId: 1})
			s.Require().NoError(err)
			return govtypesv1beta1.StatusPassed == proposalQueryResponse.Proposal.Status
		}, time.Second*30, time.Second*5, "proposal was never accepted")

		s.T().Logf("initial balance of %s of token %s is %v", s.chain.validators[2].ethereumKey.address, erc20Contract.Hex(), initialBalance)
		s.T().Log("waiting for community funds to reach destination")
		s.Require().Eventuallyf(func() bool {
			s.T().Logf("getting balance in %s of token %s", s.chain.validators[2].ethereumKey.address, erc20Contract.Hex())
			balance, err := s.getEthTokenBalanceOf(common.HexToAddress(s.chain.validators[2].ethereumKey.address), erc20Contract)
			s.Require().NoError(err, "error getting destination balance")
			s.T().Logf("balance is %v", balance)

			if balance.LT(sdk.NewInt(sendAmount)) {
				s.T().Logf("funds not received yet, dest balance: %s", balance.String())
				return false
			}

			s.Require().Equal(balance.BigInt(), sdk.NewInt(sendAmount).BigInt(), "balance was %s, expected %v", balance.String(), sendAmount)
			return true
		}, time.Second*180, time.Second*10, "community funds did not reach destination")
	})
}
