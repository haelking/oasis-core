package staking

import (
	"fmt"

	"github.com/oasislabs/oasis-core/go/common/cbor"
	"github.com/oasislabs/oasis-core/go/common/quantity"
	"github.com/oasislabs/oasis-core/go/consensus/tendermint/api"
	stakingState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/staking/state"
	staking "github.com/oasislabs/oasis-core/go/staking/api"
)

func isTransferPermitted(params *staking.ConsensusParameters, fromID staking.ID) (permitted bool) {
	permitted = true
	if params.DisableTransfers {
		permitted = false
		if params.UndisableTransfersFrom != nil && params.UndisableTransfersFrom[fromID] {
			permitted = true
		}
	}
	return
}

func (app *stakingApplication) transfer(ctx *api.Context, state *stakingState.MutableState, xfer *staking.Transfer) error {
	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	params, err := state.ConsensusParameters(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch consensus parameters: %w", err)
	}
	if err = ctx.Gas().UseGas(1, staking.GasOpTransfer, params.GasCosts); err != nil {
		return err
	}

	fromID := staking.NewIDFromPublicKey(ctx.TxSigner())
	if !isTransferPermitted(params, fromID) {
		return staking.ErrForbidden
	}

	from, err := state.Account(ctx, fromID)
	if err != nil {
		return fmt.Errorf("failed to fetch account: %w", err)
	}

	if fromID.Equal(&xfer.To) {
		// Handle transfer to self as just a balance check.
		if from.General.Balance.Cmp(&xfer.Tokens) < 0 {
			err = staking.ErrInsufficientBalance
			ctx.Logger().Error("Transfer: self-transfer greater than balance",
				"err", err,
				"from", fromID,
				"to", xfer.To,
				"amount", xfer.Tokens,
			)
			return err
		}
	} else {
		// Source and destination MUST be separate accounts with how
		// quantity.Move is implemented.
		var to *staking.Account
		to, err = state.Account(ctx, xfer.To)
		if err != nil {
			return fmt.Errorf("failed to fetch account: %w", err)
		}
		if err = quantity.Move(&to.General.Balance, &from.General.Balance, &xfer.Tokens); err != nil {
			ctx.Logger().Error("Transfer: failed to move balance",
				"err", err,
				"from", fromID,
				"to", xfer.To,
				"amount", xfer.Tokens,
			)
			return err
		}

		if err = state.SetAccount(ctx, xfer.To, to); err != nil {
			return fmt.Errorf("failed to set account: %w", err)
		}
	}

	if err = state.SetAccount(ctx, fromID, from); err != nil {
		return fmt.Errorf("failed to fetch account: %w", err)
	}

	ctx.Logger().Debug("Transfer: executed transfer",
		"from", fromID,
		"to", xfer.To,
		"amount", xfer.Tokens,
	)

	evt := &staking.TransferEvent{
		From:   fromID,
		To:     xfer.To,
		Tokens: xfer.Tokens,
	}
	ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyTransfer, cbor.Marshal(evt)))

	return nil
}

func (app *stakingApplication) burn(ctx *api.Context, state *stakingState.MutableState, burn *staking.Burn) error {
	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	params, err := state.ConsensusParameters(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch consensus parameters: %w", err)
	}
	if err = ctx.Gas().UseGas(1, staking.GasOpBurn, params.GasCosts); err != nil {
		return err
	}

	fromID := staking.NewIDFromPublicKey(ctx.TxSigner())
	from, err := state.Account(ctx, fromID)
	if err != nil {
		return fmt.Errorf("failed to fetch account: %w", err)
	}

	if err = from.General.Balance.Sub(&burn.Tokens); err != nil {
		ctx.Logger().Error("Burn: failed to burn tokens",
			"err", err,
			"from", fromID, "amount", burn.Tokens,
		)
		return err
	}

	totalSupply, err := state.TotalSupply(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch total supply: %w", err)
	}

	_ = totalSupply.Sub(&burn.Tokens)

	if err = state.SetAccount(ctx, fromID, from); err != nil {
		return fmt.Errorf("failed to set account: %w", err)
	}
	if err = state.SetTotalSupply(ctx, totalSupply); err != nil {
		return fmt.Errorf("failed to set total supply: %w", err)
	}

	ctx.Logger().Debug("Burn: burnt tokens",
		"from", fromID,
		"amount", burn.Tokens,
	)

	evt := &staking.BurnEvent{
		Owner:  fromID,
		Tokens: burn.Tokens,
	}
	ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyBurn, cbor.Marshal(evt)))

	return nil
}

func (app *stakingApplication) addEscrow(ctx *api.Context, state *stakingState.MutableState, escrow *staking.Escrow) error {
	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	params, err := state.ConsensusParameters(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch consensus parameters: %w", err)
	}
	if err = ctx.Gas().UseGas(1, staking.GasOpAddEscrow, params.GasCosts); err != nil {
		return err
	}

	// Check if sender provided at least a minimum amount of tokens.
	if escrow.Tokens.Cmp(&params.MinDelegationAmount) < 0 {
		return staking.ErrInvalidArgument
	}

	fromID := staking.NewIDFromPublicKey(ctx.TxSigner())
	from, err := state.Account(ctx, fromID)
	if err != nil {
		return fmt.Errorf("failed to fetch account: %w", err)
	}

	// Fetch escrow account.
	//
	// NOTE: Could be the same account, so make sure to not have two duplicate
	//       copies of it and overwrite it later.
	var to *staking.Account
	if fromID.Equal(&escrow.Account) {
		to = from
	} else {
		if params.DisableDelegation {
			return staking.ErrForbidden
		}
		to, err = state.Account(ctx, escrow.Account)
		if err != nil {
			return fmt.Errorf("failed to fetch account: %w", err)
		}
	}

	// Fetch delegation.
	delegation, err := state.Delegation(ctx, fromID, escrow.Account)
	if err != nil {
		return fmt.Errorf("failed to fetch delegation: %w", err)
	}

	if err = to.Escrow.Active.Deposit(&delegation.Shares, &from.General.Balance, &escrow.Tokens); err != nil {
		ctx.Logger().Error("AddEscrow: failed to escrow tokens",
			"err", err,
			"from", fromID,
			"to", escrow.Account,
			"amount", escrow.Tokens,
		)
		return err
	}

	// Commit accounts.
	if err = state.SetAccount(ctx, fromID, from); err != nil {
		return fmt.Errorf("failed to set account: %w", err)
	}
	if !fromID.Equal(&escrow.Account) {
		if err = state.SetAccount(ctx, escrow.Account, to); err != nil {
			return fmt.Errorf("failed to set account: %w", err)
		}
	}
	// Commit delegation descriptor.
	if err = state.SetDelegation(ctx, fromID, escrow.Account, delegation); err != nil {
		return fmt.Errorf("failed to set delegation: %w", err)
	}

	ctx.Logger().Debug("AddEscrow: escrowed tokens",
		"from", fromID,
		"to", escrow.Account,
		"amount", escrow.Tokens,
	)

	evt := &staking.AddEscrowEvent{
		Owner:  fromID,
		Escrow: escrow.Account,
		Tokens: escrow.Tokens,
	}
	ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyAddEscrow, cbor.Marshal(evt)))

	return nil
}

func (app *stakingApplication) reclaimEscrow(ctx *api.Context, state *stakingState.MutableState, reclaim *staking.ReclaimEscrow) error {
	// No sense if there is nothing to reclaim.
	if reclaim.Shares.IsZero() {
		return staking.ErrInvalidArgument
	}

	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	params, err := state.ConsensusParameters(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch consensus parameters: %w", err)
	}
	if err = ctx.Gas().UseGas(1, staking.GasOpReclaimEscrow, params.GasCosts); err != nil {
		return err
	}

	toID := staking.NewIDFromPublicKey(ctx.TxSigner())
	to, err := state.Account(ctx, toID)
	if err != nil {
		return fmt.Errorf("failed to fetch account: %w", err)
	}

	// Fetch escrow account.
	//
	// NOTE: Could be the same account, so make sure to not have two duplicate
	//       copies of it and overwrite it later.
	var from *staking.Account
	if toID.Equal(&reclaim.Account) {
		from = to
	} else {
		if params.DisableDelegation {
			return staking.ErrForbidden
		}
		from, err = state.Account(ctx, reclaim.Account)
		if err != nil {
			return fmt.Errorf("failed to fetch account: %w", err)
		}
	}

	// Fetch delegation.
	delegation, err := state.Delegation(ctx, toID, reclaim.Account)
	if err != nil {
		return fmt.Errorf("failed to fetch delegation: %w", err)
	}

	// Fetch debonding interval and current epoch.
	debondingInterval, err := state.DebondingInterval(ctx)
	if err != nil {
		ctx.Logger().Error("ReclaimEscrow: failed to query debonding interval",
			"err", err,
		)
		return err
	}
	epoch, err := app.state.GetEpoch(ctx, ctx.BlockHeight()+1)
	if err != nil {
		return err
	}

	deb := staking.DebondingDelegation{
		DebondEndTime: epoch + debondingInterval,
	}

	var tokens quantity.Quantity

	if err = from.Escrow.Active.Withdraw(&tokens, &delegation.Shares, &reclaim.Shares); err != nil {
		ctx.Logger().Error("ReclaimEscrow: failed to redeem escrow shares",
			"err", err,
			"to", toID,
			"from", reclaim.Account,
			"shares", reclaim.Shares,
		)
		return err
	}
	tokenAmount := tokens.Clone()

	if err = from.Escrow.Debonding.Deposit(&deb.Shares, &tokens, tokenAmount); err != nil {
		ctx.Logger().Error("ReclaimEscrow: failed to debond shares",
			"err", err,
			"to", toID,
			"from", reclaim.Account,
			"shares", reclaim.Shares,
		)
		return err
	}

	if !tokens.IsZero() {
		ctx.Logger().Error("ReclaimEscrow: inconsistency in transferring tokens from active escrow to debonding",
			"remaining", tokens,
		)
		return staking.ErrInvalidArgument
	}

	// Include the nonce as the final disambiguator to prevent overwriting debonding delegations.
	if err = state.SetDebondingDelegation(ctx, toID, reclaim.Account, to.General.Nonce, &deb); err != nil {
		return fmt.Errorf("failed to set debonding delegation: %w", err)
	}

	if err = state.SetDelegation(ctx, toID, reclaim.Account, delegation); err != nil {
		return fmt.Errorf("failed to set delegation: %w", err)
	}
	if err = state.SetAccount(ctx, toID, to); err != nil {
		return fmt.Errorf("failed to set account: %w", err)
	}
	if !toID.Equal(&reclaim.Account) {
		if err = state.SetAccount(ctx, reclaim.Account, from); err != nil {
			return fmt.Errorf("failed to set account: %w", err)
		}
	}

	return nil
}

func (app *stakingApplication) amendCommissionSchedule(
	ctx *api.Context,
	state *stakingState.MutableState,
	amendCommissionSchedule *staking.AmendCommissionSchedule,
) error {
	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	params, err := state.ConsensusParameters(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch consensus parameters: %w", err)
	}
	if err = ctx.Gas().UseGas(1, staking.GasOpAmendCommissionSchedule, params.GasCosts); err != nil {
		return err
	}

	epoch, err := app.state.GetEpoch(ctx, ctx.BlockHeight()+1)
	if err != nil {
		return err
	}

	fromID := staking.NewIDFromPublicKey(ctx.TxSigner())
	from, err := state.Account(ctx, fromID)
	if err != nil {
		return fmt.Errorf("failed to fetch account: %w", err)
	}

	if err = from.Escrow.CommissionSchedule.AmendAndPruneAndValidate(&amendCommissionSchedule.Amendment, &params.CommissionScheduleRules, epoch); err != nil {
		ctx.Logger().Error("AmendCommissionSchedule: amendment not acceptable",
			"err", err,
			"from", fromID,
		)
		return err
	}

	if err = state.SetAccount(ctx, fromID, from); err != nil {
		return fmt.Errorf("failed to set account: %w", err)
	}

	return nil
}
