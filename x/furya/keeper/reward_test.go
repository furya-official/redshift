package keeper_test

import (
	test_helpers "github.com/furya-official/furya/app"
	"github.com/furya-official/furya/x/furya/types"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	"github.com/cosmos/cosmos-sdk/x/staking/teststaking"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"
	abcitypes "github.com/tendermint/tendermint/abci/types"
)

func TestRewardPoolAndGlobalIndex(t *testing.T) {
	app, ctx := createTestContext(t)
	app.FuryaKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.FuryaAsset{
			{
				Denom:        FURYA_TOKEN_DENOM,
				RewardWeight: sdk.NewDec(2),
				TakeRate:     sdk.NewDec(0),
				TotalTokens:  sdk.ZeroInt(),
			},
			{
				Denom:        FURYA_2_TOKEN_DENOM,
				RewardWeight: sdk.NewDec(10),
				TakeRate:     sdk.NewDec(0),
				TotalTokens:  sdk.ZeroInt(),
			},
		},
	})

	// Accounts
	rewardsPoolAddr := app.AccountKeeper.GetModuleAddress(types.RewardsPoolName)
	mintPoolAddr := app.AccountKeeper.GetModuleAddress(minttypes.ModuleName)
	delegations := app.StakingKeeper.GetAllDelegations(ctx)
	valAddr1, err := sdk.ValAddressFromBech32(delegations[0].ValidatorAddress)
	require.NoError(t, err)
	val1, err := app.FuryaKeeper.GetFuryaValidator(ctx, valAddr1)
	require.NoError(t, err)
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 2, sdk.NewCoins(
		sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(1000_000)),
		sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(1000_000)),
	))
	user1 := addrs[0]
	user2 := addrs[1]

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(4000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake2", sdk.NewInt(4000_000))))
	require.NoError(t, err)
	coin := app.BankKeeper.GetBalance(ctx, mintPoolAddr, "stake")
	require.Equal(t, sdk.NewCoin("stake", sdk.NewInt(4000_000)), coin)

	// Transfer to reward pool without delegations will fail
	err = app.FuryaKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(2000_000))))
	require.Error(t, err)

	_, err = app.FuryaKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(1000_000)))
	require.NoError(t, err)
	assets := app.FuryaKeeper.GetAllAssets(ctx)
	err = app.FuryaKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.FuryaKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(2000_000))))
	require.NoError(t, err)

	// Expect rewards pool to have something
	balance := app.BankKeeper.GetBalance(ctx, rewardsPoolAddr, "stake")
	require.Equal(t, sdk.NewCoin("stake", sdk.NewInt(2000_000)), balance)

	// Expect validator global index to be updated
	require.NoError(t, err)
	globalIndices := types.NewRewardHistories(val1.GlobalRewardHistory)
	require.Equal(t, types.RewardHistories{
		types.RewardHistory{
			Denom: "stake",
			Index: sdk.NewDec(1),
		},
	}, globalIndices)

	// New delegation from user 2
	_, err = app.FuryaKeeper.Delegate(ctx, user2, val1, sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(1000_000)))
	require.NoError(t, err)
	assets = app.FuryaKeeper.GetAllAssets(ctx)
	err = app.FuryaKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.FuryaKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(2000_000))))
	require.NoError(t, err)

	globalIndices = types.NewRewardHistories(val1.GlobalRewardHistory)
	require.Equal(t, types.RewardHistories{
		types.RewardHistory{
			Denom: "stake",
			Index: sdk.NewDec(14).Quo(sdk.NewDec(12)),
		},
	}, globalIndices)

	// Transfer another token to reward pool
	err = app.FuryaKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake2", sdk.NewInt(4000_000))))
	require.NoError(t, err)

	// Expect global index to be updated
	// 14/12 + 4/12 = 18/12
	globalIndices = types.NewRewardHistories(val1.GlobalRewardHistory)
	require.Equal(t, types.RewardHistories{
		types.RewardHistory{
			Denom: "stake",
			Index: sdk.NewDec(14).Quo(sdk.NewDec(12)),
		},
		types.RewardHistory{
			Denom: "stake2",
			Index: sdk.NewDec(4).Quo(sdk.NewDec(12)),
		},
	}, globalIndices)
}

func TestClaimRewards(t *testing.T) {
	app, ctx := createTestContext(t)
	app.FuryaKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.FuryaAsset{
			types.NewFuryaAsset(FURYA_TOKEN_DENOM, sdk.NewDec(2), sdk.NewDec(0), ctx.BlockTime()),
			types.NewFuryaAsset(FURYA_2_TOKEN_DENOM, sdk.NewDec(10), sdk.NewDec(0), ctx.BlockTime()),
		},
	})

	// Accounts
	mintPoolAddr := app.AccountKeeper.GetModuleAddress(minttypes.ModuleName)
	rewardsPoolAddr := app.AccountKeeper.GetModuleAddress(types.RewardsPoolName)
	delegations := app.StakingKeeper.GetAllDelegations(ctx)
	valAddr1, err := sdk.ValAddressFromBech32(delegations[0].ValidatorAddress)
	require.NoError(t, err)
	val1, err := app.FuryaKeeper.GetFuryaValidator(ctx, valAddr1)
	require.NoError(t, err)
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 2, sdk.NewCoins(
		sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(1000_000)),
		sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(1000_000)),
	))
	user1 := addrs[0]
	user2 := addrs[1]

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(4000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake2", sdk.NewInt(4000_000))))
	require.NoError(t, err)

	// New delegation from user 1
	_, err = app.FuryaKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(1000_000)))
	require.NoError(t, err)
	assets := app.FuryaKeeper.GetAllAssets(ctx)
	err = app.FuryaKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.FuryaKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(2000_000))))
	require.NoError(t, err)

	// New delegation from user 2
	_, err = app.FuryaKeeper.Delegate(ctx, user2, val1, sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(1000_000)))
	require.NoError(t, err)
	assets = app.FuryaKeeper.GetAllAssets(ctx)
	err = app.FuryaKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Transfer to reward pool
	err = app.FuryaKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(2000_000))))
	require.NoError(t, err)

	asset, _ := app.FuryaKeeper.GetAssetByDenom(ctx, FURYA_TOKEN_DENOM)
	require.Equal(t,
		sdk.NewInt(1000_000),
		val1.TotalTokensWithAsset(asset).TruncateInt(),
	)
	asset, _ = app.FuryaKeeper.GetAssetByDenom(ctx, FURYA_2_TOKEN_DENOM)
	require.Equal(t,
		sdk.NewInt(1000_000),
		val1.TotalTokensWithAsset(asset).TruncateInt(),
	)

	// Transfer another token to reward pool
	err = app.FuryaKeeper.AddAssetsToRewardPool(ctx, mintPoolAddr, val1, sdk.NewCoins(sdk.NewCoin("stake2", sdk.NewInt(4000_000))))
	require.NoError(t, err)

	// Make sure reward indices are right
	require.Equal(t,
		types.NewRewardHistories([]types.RewardHistory{
			{
				Denom: "stake",
				Index: sdk.MustNewDecFromStr("1.166666666666666667"),
			},
			{
				Denom: "stake2",
				Index: sdk.MustNewDecFromStr("0.333333333333333333"),
			},
		}),
		types.NewRewardHistories(val1.GlobalRewardHistory),
	)

	// before claiming, there should be tokens in rewards pool
	coins := app.BankKeeper.GetAllBalances(ctx, rewardsPoolAddr)
	require.Equal(t,
		sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(4000_000)), sdk.NewCoin("stake2", sdk.NewInt(4000_000))),
		coins,
	)

	// User 1 claims rewards
	// User 1 has 1 STAKE (2 Power)
	// Added 2 stake rewards (fully belonging to user 1)
	// User 2 has 1 STAKE (10 Power)
	// Added 2 stake rewards (user1: 2/12 * 2, user2: 10/12 * 2)
	// Added 4 stake2 rewards (user1: 2/12 * 4, user2: 10/12 * 4)
	coins, err = app.FuryaKeeper.ClaimDelegationRewards(ctx, user1, val1, FURYA_TOKEN_DENOM)
	require.NoError(t, err)
	require.Equal(t, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(2_333_333)), sdk.NewCoin("stake2", sdk.NewInt(666_666))), coins)

	// User 2 claims rewards but doesn't use the right denom
	_, err = app.FuryaKeeper.ClaimDelegationRewards(ctx, user2, val1, FURYA_TOKEN_DENOM)
	require.Error(t, err)

	// User 2 claims rewards
	coins, err = app.FuryaKeeper.ClaimDelegationRewards(ctx, user2, val1, FURYA_2_TOKEN_DENOM)
	require.NoError(t, err)
	require.Equal(t, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(1_666_666)), sdk.NewCoin("stake2", sdk.NewInt(3_333_333))), coins)

	// After claiming, there should be nothing left in rewards pool
	// Some rounding left
	coins = app.BankKeeper.GetAllBalances(ctx, rewardsPoolAddr)
	require.Equal(t, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(1)), sdk.NewCoin("stake2", sdk.NewInt(1))), coins)

	// Global indices
	require.NoError(t, err)
	indices := types.NewRewardHistories(val1.GlobalRewardHistory)

	// Check that all delegations have updated local indices
	delegation, found := app.FuryaKeeper.GetDelegation(ctx, user1, val1, FURYA_TOKEN_DENOM)
	require.True(t, found)
	require.Equal(t, indices, types.NewRewardHistories(delegation.RewardHistory))

	delegation, found = app.FuryaKeeper.GetDelegation(ctx, user2, val1, FURYA_2_TOKEN_DENOM)
	require.True(t, found)
	require.Equal(t, indices, types.NewRewardHistories(delegation.RewardHistory))
}

func TestClaimRewardsWithMultipleValidators(t *testing.T) {
	var err error
	app, ctx := createTestContext(t)
	startTime := time.Now()
	ctx = ctx.WithBlockTime(startTime)
	app.FuryaKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.FuryaAsset{
			types.NewFuryaAsset(FURYA_TOKEN_DENOM, sdk.NewDec(2), sdk.NewDec(0), startTime),
			types.NewFuryaAsset(FURYA_2_TOKEN_DENOM, sdk.NewDec(10), sdk.NewDec(0), startTime),
		},
	})

	// Set tax and rewards to be zero for easier calculation
	distParams := app.DistrKeeper.GetParams(ctx)
	distParams.CommunityTax = sdk.ZeroDec()
	distParams.BaseProposerReward = sdk.ZeroDec()
	distParams.BonusProposerReward = sdk.ZeroDec()
	app.DistrKeeper.SetParams(ctx, distParams)

	// Accounts
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 4, sdk.NewCoins(
		sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(1000_000)),
		sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(1000_000)),
	))
	pks := test_helpers.CreateTestPubKeys(2)

	// Creating two validators: 1 with 0% commission, 1 with 100% commission
	valAddr1 := sdk.ValAddress(addrs[0])
	_val1 := teststaking.NewValidator(t, valAddr1, pks[0])
	_val1.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdk.NewDec(0),
			MaxRate:       sdk.NewDec(0),
			MaxChangeRate: sdk.NewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val1)

	valAddr2 := sdk.ValAddress(addrs[1])
	_val2 := teststaking.NewValidator(t, valAddr2, pks[1])
	_val2.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdk.NewDec(1),
			MaxRate:       sdk.NewDec(1),
			MaxChangeRate: sdk.NewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val2)

	user1 := addrs[2]
	user2 := addrs[3]

	// Mint tokens
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(4000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin("stake2", sdk.NewInt(4000_000))))
	require.NoError(t, err)

	// New delegation from user 1 to val 1
	val1, _ := app.FuryaKeeper.GetFuryaValidator(ctx, valAddr1)
	_, err = app.FuryaKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(1000_000)))
	require.NoError(t, err)

	// New delegation from user 2 to val 2
	val2, _ := app.FuryaKeeper.GetFuryaValidator(ctx, valAddr2)
	_, err = app.FuryaKeeper.Delegate(ctx, user2, val2, sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(1000_000)))
	require.NoError(t, err)

	assets := app.FuryaKeeper.GetAllAssets(ctx)
	err = app.FuryaKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)
	// Check total bonded amount
	require.Equal(t, sdk.NewInt(13_000_000), app.StakingKeeper.TotalBondedTokens(ctx))

	// Transfer to rewards to fee pool to be distributed
	app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin("stake", sdk.NewInt(4000_000))))

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	cons1, _ := val1.GetConsAddr()
	cons2, _ := val2.GetConsAddr()
	var votingPower int64 = 12
	app.DistrKeeper.AllocateTokens(ctx, votingPower, votingPower, cons1, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   2,
			},
			SignedLastBlock: true,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   10,
			},
			SignedLastBlock: true,
		},
	})

	commission := app.DistrKeeper.GetValidatorAccumulatedCommission(ctx, val1.GetOperator()).Commission
	require.Equal(t, sdk.NewInt(0), commission.AmountOf("stake").TruncateInt())
	commission = app.DistrKeeper.GetValidatorAccumulatedCommission(ctx, val2.GetOperator()).Commission
	require.Equal(t, sdk.NewInt(3333333), commission.AmountOf("stake").TruncateInt())

	rewards := app.DistrKeeper.GetValidatorCurrentRewards(ctx, val1.GetOperator()).Rewards
	require.Equal(t, sdk.NewInt(666666), rewards.AmountOf("stake").TruncateInt())
	rewards = app.DistrKeeper.GetValidatorCurrentRewards(ctx, val2.GetOperator()).Rewards
	require.Equal(t, sdk.NewInt(0), rewards.AmountOf("stake").TruncateInt())

	// User 1 should be getting all the rewards from validator 1 since it has 0 commission
	coins, err := app.FuryaKeeper.ClaimDelegationRewards(ctx, user1, val1, FURYA_TOKEN_DENOM)
	require.NoError(t, err)
	require.Equal(t, sdk.NewInt(666666), coins.AmountOf("stake"))

	// User 2 should be getting no rewards since validator 2 has 100% commission
	coins, err = app.FuryaKeeper.ClaimDelegationRewards(ctx, user2, val2, FURYA_2_TOKEN_DENOM)
	require.NoError(t, err)
	require.Equal(t, sdk.NewInt(0), coins.AmountOf("stake"))
}

func TestClaimRewardsAfterRewardsRatesChange(t *testing.T) {
	var err error
	app, ctx := createTestContext(t)
	ctx = ctx.WithBlockHeight(1)
	app.FuryaKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.FuryaAsset{
			types.NewFuryaAsset(FURYA_TOKEN_DENOM, sdk.NewDec(2), sdk.NewDec(0), ctx.BlockTime()),
			types.NewFuryaAsset(FURYA_2_TOKEN_DENOM, sdk.NewDec(10), sdk.NewDec(0), ctx.BlockTime()),
		},
	})

	// Set tax and rewards to be zero for easier calculation
	distParams := app.DistrKeeper.GetParams(ctx)
	distParams.CommunityTax = sdk.ZeroDec()
	distParams.BaseProposerReward = sdk.ZeroDec()
	distParams.BonusProposerReward = sdk.ZeroDec()
	app.DistrKeeper.SetParams(ctx, distParams)

	// Accounts
	bondDenom := app.StakingKeeper.BondDenom(ctx)
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 4, sdk.NewCoins(
		sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(10_000_000)),
		sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(10_000_000)),
	))

	// Creating two validators: 1 with 0% commission, 1 with 100% commission
	pks := test_helpers.CreateTestPubKeys(2)
	valAddr1 := sdk.ValAddress(addrs[0])
	_val1 := teststaking.NewValidator(t, valAddr1, pks[0])
	_val1.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdk.NewDec(0),
			MaxRate:       sdk.NewDec(0),
			MaxChangeRate: sdk.NewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val1)
	val1, err := app.FuryaKeeper.GetFuryaValidator(ctx, valAddr1)
	require.NoError(t, err)

	valAddr2 := sdk.ValAddress(addrs[1])
	_val2 := teststaking.NewValidator(t, valAddr2, pks[1])
	_val2.Commission = stakingtypes.Commission{
		CommissionRates: stakingtypes.CommissionRates{
			Rate:          sdk.NewDec(0),
			MaxRate:       sdk.NewDec(1),
			MaxChangeRate: sdk.NewDec(0),
		},
		UpdateTime: time.Now(),
	}
	test_helpers.RegisterNewValidator(t, app, ctx, _val2)
	val2, err := app.FuryaKeeper.GetFuryaValidator(ctx, valAddr2)
	require.NoError(t, err)

	user1 := addrs[2]
	user2 := addrs[3]

	// New delegations
	app.FuryaKeeper.Delegate(ctx, user1, val1, sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(1000_000)))
	app.FuryaKeeper.Delegate(ctx, user2, val2, sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(1000_000)))
	assets := app.FuryaKeeper.GetAllAssets(ctx)
	err = app.FuryaKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Accumulate rewards in pool and distribute it
	err = app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, sdk.NewCoins(sdk.NewCoin(bondDenom, sdk.NewInt(40_000_000))))
	require.NoError(t, err)
	err = app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin(bondDenom, sdk.NewInt(10_000_000))))
	require.NoError(t, err)

	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	val1, _ = app.FuryaKeeper.GetFuryaValidator(ctx, valAddr1)
	cons1, _ := val1.GetConsAddr()
	power1 := val1.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))

	val2, _ = app.FuryaKeeper.GetFuryaValidator(ctx, valAddr2)
	cons2, _ := val2.GetConsAddr()
	power2 := val2.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))

	app.DistrKeeper.AllocateTokens(ctx, power1+power2, power1+power2, cons1, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   power1,
			},
			SignedLastBlock: true,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   power2,
			},
			SignedLastBlock: true,
		},
	})

	err = app.FuryaKeeper.UpdateFuryaAsset(ctx, types.NewFuryaAsset(FURYA_TOKEN_DENOM, sdk.NewDec(10), sdk.NewDec(0), ctx.BlockTime()))
	require.NoError(t, err)
	assets = app.FuryaKeeper.GetAllAssets(ctx)
	err = app.FuryaKeeper.RebalanceBondTokenWeights(ctx, assets)
	require.NoError(t, err)

	// Expect reward change snapshots to be taken
	val1, _ = app.FuryaKeeper.GetFuryaValidator(ctx, valAddr1)
	iter := app.FuryaKeeper.IterateWeightChangeSnapshot(ctx, FURYA_TOKEN_DENOM, valAddr1, 0)
	var snapshot types.RewardWeightChangeSnapshot
	require.True(t, iter.Valid())
	app.AppCodec().MustUnmarshal(iter.Value(), &snapshot)
	require.Equal(t, types.RewardWeightChangeSnapshot{
		PrevRewardWeight: sdk.NewDec(2),
		RewardHistories:  val1.GlobalRewardHistory,
	}, snapshot)
	iter.Close()

	// Accumulate rewards in pool
	err = app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin(bondDenom, sdk.NewInt(10_000_000))))
	require.NoError(t, err)

	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	val1, _ = app.FuryaKeeper.GetFuryaValidator(ctx, valAddr1)
	power1 = val1.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))

	val2, _ = app.FuryaKeeper.GetFuryaValidator(ctx, valAddr2)
	power2 = val2.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))
	app.DistrKeeper.AllocateTokens(ctx, power1+power2, power1+power2, cons1, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   power1,
			},
			SignedLastBlock: true,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   power2,
			},
			SignedLastBlock: true,
		},
	})

	rewards1, err := app.FuryaKeeper.ClaimDelegationRewards(ctx, user1, val1, FURYA_TOKEN_DENOM)
	require.NoError(t, err)
	require.Equal(t, sdk.NewInt(5_000_000+1_666_666), rewards1.AmountOf(bondDenom))

	rewards2, err := app.FuryaKeeper.ClaimDelegationRewards(ctx, user2, val2, FURYA_2_TOKEN_DENOM)
	require.NoError(t, err)
	require.Equal(t, sdk.NewInt(5_000_000+8_333_333), rewards2.AmountOf(bondDenom))

	// Accumulate rewards in pool
	err = app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, authtypes.FeeCollectorName, sdk.NewCoins(sdk.NewCoin(bondDenom, sdk.NewInt(10_000_000))))
	require.NoError(t, err)

	// Distribute in the next begin block
	// At the next begin block, tokens will be distributed from the fee pool
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1)
	val1, _ = app.FuryaKeeper.GetFuryaValidator(ctx, valAddr1)
	power1 = val1.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))

	val2, _ = app.FuryaKeeper.GetFuryaValidator(ctx, valAddr2)
	power2 = val2.ConsensusPower(app.StakingKeeper.PowerReduction(ctx))
	app.DistrKeeper.AllocateTokens(ctx, power1+power2, power1+power2, cons1, []abcitypes.VoteInfo{
		{
			Validator: abcitypes.Validator{
				Address: cons1,
				Power:   power1,
			},
			SignedLastBlock: true,
		},
		{
			Validator: abcitypes.Validator{
				Address: cons2,
				Power:   power2,
			},
			SignedLastBlock: true,
		},
	})

	rewards1, err = app.FuryaKeeper.ClaimDelegationRewards(ctx, user1, val1, FURYA_TOKEN_DENOM)
	require.NoError(t, err)
	require.Equal(t, sdk.NewInt(5_000_000), rewards1.AmountOf(bondDenom))

	rewards2, err = app.FuryaKeeper.ClaimDelegationRewards(ctx, user2, val2, FURYA_2_TOKEN_DENOM)
	require.NoError(t, err)
	require.Equal(t, sdk.NewInt(5_000_000), rewards2.AmountOf(bondDenom))
}

func TestRewardClaimingAfterRatesDecay(t *testing.T) {
	var err error
	app, ctx := createTestContext(t)
	bondDenom := app.StakingKeeper.BondDenom(ctx)
	startTime := time.Now().UTC()
	ctx = ctx.WithBlockTime(startTime).WithBlockHeight(1)
	app.FuryaKeeper.InitGenesis(ctx, &types.GenesisState{
		Params: types.DefaultParams(),
		Assets: []types.FuryaAsset{},
	})
	rewardStartDelay := app.FuryaKeeper.RewardDelayTime(ctx)

	// Set tax and rewards to be zero for easier calculation
	distParams := app.DistrKeeper.GetParams(ctx)
	distParams.CommunityTax = sdk.ZeroDec()
	distParams.BaseProposerReward = sdk.ZeroDec()
	distParams.BonusProposerReward = sdk.ZeroDec()
	app.DistrKeeper.SetParams(ctx, distParams)

	// Accounts
	addrs := test_helpers.AddTestAddrsIncremental(app, ctx, 5, sdk.NewCoins(
		sdk.NewCoin(bondDenom, sdk.NewInt(1_000_000_000_000)),
		sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(5_000_000)),
		sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(5_000_000)),
	))

	// Increase the stake on genesis validator
	delegations := app.StakingKeeper.GetAllDelegations(ctx)
	require.Len(t, delegations, 1)
	valAddr0, err := sdk.ValAddressFromBech32(delegations[0].ValidatorAddress)
	require.NoError(t, err)
	_val0, _ := app.StakingKeeper.GetValidator(ctx, valAddr0)
	_, err = app.StakingKeeper.Delegate(ctx, addrs[4], sdk.NewInt(9_000_000), stakingtypes.Unbonded, _val0, true)
	require.NoError(t, err)

	val0, _ := app.FuryaKeeper.GetFuryaValidator(ctx, _val0.GetOperator())
	require.NoError(t, err)

	// Pass a proposal to add a new asset with a huge decay rate
	decayInterval := time.Minute
	decayRate := sdk.MustNewDecFromStr("0.5")
	app.FuryaKeeper.CreateFurya(ctx, &types.MsgCreateFuryaProposal{
		Title:                "",
		Description:          "",
		Denom:                FURYA_TOKEN_DENOM,
		RewardWeight:         sdk.NewDec(1),
		TakeRate:             sdk.ZeroDec(),
		RewardChangeRate:     decayRate,
		RewardChangeInterval: decayInterval,
	})

	// Pass a proposal to add another new asset no decay
	app.FuryaKeeper.CreateFurya(ctx, &types.MsgCreateFuryaProposal{
		Title:                "",
		Description:          "",
		Denom:                FURYA_2_TOKEN_DENOM,
		RewardWeight:         sdk.NewDec(1),
		TakeRate:             sdk.ZeroDec(),
		RewardChangeRate:     sdk.OneDec(),
		RewardChangeInterval: time.Duration(0),
	})

	// Delegate to validator
	_, err = app.FuryaKeeper.Delegate(ctx, addrs[1], val0, sdk.NewCoin(FURYA_TOKEN_DENOM, sdk.NewInt(5_000_000)))
	require.NoError(t, err)

	_, err = app.FuryaKeeper.Delegate(ctx, addrs[1], val0, sdk.NewCoin(FURYA_2_TOKEN_DENOM, sdk.NewInt(5_000_000)))
	require.NoError(t, err)
	//
	assets := app.FuryaKeeper.GetAllAssets(ctx)
	err = app.FuryaKeeper.RebalanceHook(ctx, assets)
	require.NoError(t, err)

	// Move block time to trigger 2 decays
	ctx = ctx.WithBlockTime(ctx.BlockTime().Add(decayInterval * 2).Add(rewardStartDelay)).WithBlockHeight(ctx.BlockHeight() + 1)
	app.FuryaKeeper.AddAssetsToRewardPool(ctx, addrs[0], val0, sdk.NewCoins(sdk.NewCoin(bondDenom, sdk.NewInt(1000_000))))
	assets = app.FuryaKeeper.GetAllAssets(ctx)

	// Running the decay hook should update reward weight
	app.FuryaKeeper.RewardWeightChangeHook(ctx, assets)
	asset, _ := app.FuryaKeeper.GetAssetByDenom(ctx, FURYA_TOKEN_DENOM)
	require.Equal(t, sdk.MustNewDecFromStr("0.25"), asset.RewardWeight)
	app.FuryaKeeper.AddAssetsToRewardPool(ctx, addrs[0], val0, sdk.NewCoins(sdk.NewCoin(bondDenom, sdk.NewInt(1000_000))))

	coins, err := app.FuryaKeeper.ClaimDelegationRewards(ctx, addrs[1], val0, FURYA_TOKEN_DENOM)
	coins2, err := app.FuryaKeeper.ClaimDelegationRewards(ctx, addrs[1], val0, FURYA_2_TOKEN_DENOM)

	// Expect total claimed rewards to be whatever that was added
	require.Equal(t, sdk.NewInt(2000_000), coins.Add(coins2...).AmountOf(bondDenom))
}
