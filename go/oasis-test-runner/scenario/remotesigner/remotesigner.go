// Package remotesigner implements the Oasis remote-signer test scenarios.
package remotesigner

import (
	flag "github.com/spf13/pflag"

	"github.com/oasislabs/oasis-core/go/common/logging"
	"github.com/oasislabs/oasis-core/go/oasis-test-runner/cmd"
	"github.com/oasislabs/oasis-core/go/oasis-test-runner/env"
	"github.com/oasislabs/oasis-core/go/oasis-test-runner/oasis"
	"github.com/oasislabs/oasis-core/go/oasis-test-runner/scenario"
)

const (
	cfgServerBinary = "binary"
)

var (
	// RemoteSignerParamsDummy is a dummy instance of remoteSignerImpl used to register remote-signer/* parameters.
	RemoteSignerParamsDummy *remoteSignerImpl = newRemoteSignerImpl("")
)

type remoteSignerImpl struct {
	name   string
	logger *logging.Logger

	flags *flag.FlagSet
}

func newRemoteSignerImpl(name string) *remoteSignerImpl {
	// Empty scenario name is used for registering global parameters only.
	fullName := "remote-signer"
	if name != "" {
		fullName += "/" + name
	}

	sc := &remoteSignerImpl{
		name:   fullName,
		logger: logging.GetLogger("scenario/remote-signer/" + name),
		flags:  flag.NewFlagSet(fullName, flag.ContinueOnError),
	}
	// path to remote-signer server executable.
	sc.flags.String(cfgServerBinary, "oasis-remote-signer", "runtime binary")

	return sc
}

func (sc *remoteSignerImpl) Clone() remoteSignerImpl {
	newSc := remoteSignerImpl{
		name:   sc.name,
		logger: sc.logger,
		flags:  flag.NewFlagSet(sc.name, flag.ContinueOnError),
	}
	newSc.flags.AddFlagSet(sc.flags)

	return newSc
}

func (sc *remoteSignerImpl) Name() string {
	return sc.name
}

func (sc *remoteSignerImpl) Parameters() *flag.FlagSet {
	return sc.flags
}

func (sc *remoteSignerImpl) PreInit(childEnv *env.Env) error {
	return nil
}

func (sc *remoteSignerImpl) Fixture() (*oasis.NetworkFixture, error) {
	return nil, nil
}

func (sc *remoteSignerImpl) Init(childEnv *env.Env, net *oasis.Network) error {
	return nil
}

// RegisterScenarios registers all scenarios for remote-signer.
func RegisterScenarios() error {
	// Register non-scenario-specific parameters.
	cmd.RegisterTestParams(RemoteSignerParamsDummy.Name(), RemoteSignerParamsDummy.Parameters())

	// Register default scenarios which are executed, if no test names provided.
	for _, s := range []scenario.Scenario{
		// Basic remote signer test case.
		Basic,
	} {
		if err := cmd.Register(s); err != nil {
			return err
		}
	}

	return nil
}
