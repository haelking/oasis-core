package staking

import (
	"encoding/hex"
	"fmt"

	"github.com/tendermint/tendermint/abci/types"

	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
	abciAPI "github.com/oasislabs/oasis-core/go/consensus/tendermint/api"
	registryState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/registry/state"
	stakingState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/staking/state"
	epochtime "github.com/oasislabs/oasis-core/go/epochtime/api"
)

func (app *stakingApplication) resolveEntityIDFromProposer(
	ctx *abciAPI.Context,
	regState *registryState.MutableState,
	request types.RequestBeginBlock,
) *signature.PublicKey {
	var proposingEntity *signature.PublicKey
	proposerNode, err := regState.NodeByConsensusAddress(ctx, request.Header.ProposerAddress)
	if err != nil {
		ctx.Logger().Warn("failed to get proposer node",
			"err", err,
			"address", hex.EncodeToString(request.Header.ProposerAddress),
		)
	} else {
		proposingEntity = &proposerNode.EntityID
	}
	return proposingEntity
}

func (app *stakingApplication) rewardBlockProposing(
	ctx *abciAPI.Context,
	stakeState *stakingState.MutableState,
	proposingEntity *signature.PublicKey,
	numEligibleValidators, numSigningEntities int,
) error {
	if proposingEntity == nil {
		return nil
	}

	params, err := stakeState.ConsensusParameters(ctx)
	if err != nil {
		return fmt.Errorf("staking mutable state getting consensus parameters: %w", err)
	}

	epoch, err := app.state.GetCurrentEpoch(ctx)
	if err != nil {
		return fmt.Errorf("app state getting current epoch: %w", err)
	}
	invalidEpoch := epochtime.EpochInvalid // Workaround for incorrect go-fuzz instrumentation.
	if epoch == invalidEpoch {
		ctx.Logger().Info("rewardBlockProposing: this block does not belong to an epoch. no block proposing reward")
		return nil
	}
	// Reward the proposer based on the `(number of included votes) / (size of the validator set)` ratio.
	if err = stakeState.AddRewardSingleAttenuated(ctx, epoch, &params.RewardFactorBlockProposed, numSigningEntities, numEligibleValidators, *proposingEntity); err != nil {
		return fmt.Errorf("adding rewards: %w", err)
	}
	return nil
}
