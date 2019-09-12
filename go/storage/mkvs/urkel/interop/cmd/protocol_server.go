package cmd

import (
	"io/ioutil"
	"os"

	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"

	memorySigner "github.com/oasislabs/ekiden/go/common/crypto/signature/signers/memory"
	"github.com/oasislabs/ekiden/go/common/grpc"
	"github.com/oasislabs/ekiden/go/common/identity"
	"github.com/oasislabs/ekiden/go/common/logging"
	"github.com/oasislabs/ekiden/go/ekiden/cmd/common/background"
	"github.com/oasislabs/ekiden/go/storage"
	storageApi "github.com/oasislabs/ekiden/go/storage/api"
	"github.com/oasislabs/ekiden/go/storage/database"
)

const (
	cfgServerSocket = "socket"
)

var (
	protoServerFlags = flag.NewFlagSet("", flag.ContinueOnError)

	protoServerCmd = &cobra.Command{
		Use:   "proto-server",
		Short: "run simple gRPC server implementing the storage service",
		Run:   doProtoServer,
	}

	logger = logging.GetLogger("cmd/protocol_server")
)

func doProtoServer(cmd *cobra.Command, args []string) {
	svcMgr := background.NewServiceManager(logger)
	defer svcMgr.Cleanup()

	// Create a new random temporary directory under /tmp.
	dataDir, err := ioutil.TempDir("", "ekiden-storage-protocol-server-")
	if err != nil {
		logger.Error("failed to create data directory",
			"err", err,
		)
		return
	}
	defer os.RemoveAll(dataDir)

	// Generate dummy identity.
	ident, err := identity.LoadOrGenerate(dataDir, memorySigner.NewFactory())
	if err != nil {
		logger.Error("failed to generate identity",
			"err", err,
		)
		return
	}

	// Initialize the gRPC server.
	config := &grpc.ServerConfig{
		Name:           "protocol_server",
		Path:           viper.GetString(cfgServerSocket),
		InstallWrapper: false,
	}

	grpcSrv, err := grpc.NewServer(config)
	if err != nil {
		logger.Error("failed to initialize gRPC server",
			"err", err,
		)
		return
	}
	svcMgr.Register(grpcSrv)

	// Initialize a dummy storage backend.
	storageCfg := storageApi.Config{
		Backend:            database.BackendNameLevelDB,
		DB:                 dataDir,
		Signer:             ident.NodeSigner,
		ApplyLockLRUSlots:  1,
		InsecureSkipChecks: false,
	}
	backend, err := database.New(&storageCfg)
	if err != nil {
		logger.Error("failed to initialize storage backend",
			"err", err,
		)
		return
	}
	storage.NewGRPCServer(grpcSrv.Server(), backend, &grpc.AllowAllRuntimePolicyChecker{}, false)

	// Start the gRPC server.
	if err := grpcSrv.Start(); err != nil {
		logger.Error("failed to start gRPC server",
			"err", err,
		)
		return
	}

	logger.Info("initialization complete: ready to serve")

	// Wait for the services to catch on fire or otherwise
	// terminate.
	svcMgr.Wait()
}

// Register registers the grpc-server sub-command and all of it's children.
func RegisterProtoServer(parentCmd *cobra.Command) {
	protoServerCmd.Flags().AddFlagSet(protoServerFlags)

	parentCmd.AddCommand(protoServerCmd)
}

func init() {
	protoServerFlags.String(cfgServerSocket, "storage.sock", "path to storage protocol server socket")
	_ = viper.BindPFlags(protoServerFlags)
}
