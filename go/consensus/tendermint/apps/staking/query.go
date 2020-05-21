package staking

import (
	"context"

	"github.com/oasislabs/oasis-core/go/common/quantity"
	stakingState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/staking/state"
	epochtime "github.com/oasislabs/oasis-core/go/epochtime/api"
	staking "github.com/oasislabs/oasis-core/go/staking/api"
)

// Query is the staking query interface.
type Query interface {
	TotalSupply(context.Context) (*quantity.Quantity, error)
	CommonPool(context.Context) (*quantity.Quantity, error)
	LastBlockFees(context.Context) (*quantity.Quantity, error)
	Threshold(context.Context, staking.ThresholdKind) (*quantity.Quantity, error)
	DebondingInterval(context.Context) (epochtime.EpochTime, error)
	Accounts(context.Context) ([]staking.ID, error)
	AccountInfo(context.Context, staking.ID) (*staking.Account, error)
	Delegations(context.Context, staking.ID) (map[staking.ID]*staking.Delegation, error)
	DebondingDelegations(context.Context, staking.ID) (map[staking.ID][]*staking.DebondingDelegation, error)
	Genesis(context.Context) (*staking.Genesis, error)
	ConsensusParameters(context.Context) (*staking.ConsensusParameters, error)
}

// QueryFactory is the staking query factory.
type QueryFactory struct {
	app *stakingApplication
}

// QueryAt returns the staking query interface for a specific height.
func (sf *QueryFactory) QueryAt(ctx context.Context, height int64) (Query, error) {
	state, err := stakingState.NewImmutableState(ctx, sf.app.state, height)
	if err != nil {
		return nil, err
	}
	return &stakingQuerier{state}, nil
}

type stakingQuerier struct {
	state *stakingState.ImmutableState
}

func (sq *stakingQuerier) TotalSupply(ctx context.Context) (*quantity.Quantity, error) {
	return sq.state.TotalSupply(ctx)
}

func (sq *stakingQuerier) CommonPool(ctx context.Context) (*quantity.Quantity, error) {
	return sq.state.CommonPool(ctx)
}

func (sq *stakingQuerier) LastBlockFees(ctx context.Context) (*quantity.Quantity, error) {
	return sq.state.LastBlockFees(ctx)
}

func (sq *stakingQuerier) Threshold(ctx context.Context, kind staking.ThresholdKind) (*quantity.Quantity, error) {
	thresholds, err := sq.state.Thresholds(ctx)
	if err != nil {
		return nil, err
	}

	threshold, ok := thresholds[kind]
	if !ok {
		return nil, staking.ErrInvalidThreshold
	}
	return &threshold, nil
}

func (sq *stakingQuerier) DebondingInterval(ctx context.Context) (epochtime.EpochTime, error) {
	return sq.state.DebondingInterval(ctx)
}

func (sq *stakingQuerier) Accounts(ctx context.Context) ([]staking.ID, error) {
	return sq.state.Accounts(ctx)
}

func (sq *stakingQuerier) AccountInfo(ctx context.Context, id staking.ID) (*staking.Account, error) {
	return sq.state.Account(ctx, id)
}

func (sq *stakingQuerier) Delegations(ctx context.Context, id staking.ID) (map[staking.ID]*staking.Delegation, error) {
	return sq.state.DelegationsFor(ctx, id)
}

func (sq *stakingQuerier) DebondingDelegations(ctx context.Context, id staking.ID) (map[staking.ID][]*staking.DebondingDelegation, error) {
	return sq.state.DebondingDelegationsFor(ctx, id)
}

func (sq *stakingQuerier) ConsensusParameters(ctx context.Context) (*staking.ConsensusParameters, error) {
	return sq.state.ConsensusParameters(ctx)
}

func (app *stakingApplication) QueryFactory() interface{} {
	return &QueryFactory{app}
}
