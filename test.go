package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	tcfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/libs/log"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmp2p "github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
	tmtime "github.com/tendermint/tendermint/types/time"

	"github.com/testground/sdk-go/network"
	"github.com/testground/sdk-go/run"
	"github.com/testground/sdk-go/runtime"
	tgsync "github.com/testground/sdk-go/sync"

	"github.com/dymensionxyz/dymint/config"
	"github.com/dymensionxyz/dymint/conv"
	"github.com/dymensionxyz/dymint/node"
)

// setupNetwork instructs the sidecar (if enabled) to setup the network for this
// test case.
func setupNetwork(ctx context.Context, runenv *runtime.RunEnv, netclient *network.Client, latencyMin int, latencyMax int, bandwidth int) (*network.Config, error) {
	if !runenv.TestSidecar {
		return nil, nil
	}

	// Wait for the network to be initialized.
	runenv.RecordMessage("Waiting for network initialization")
	err := netclient.WaitNetworkInitialized(ctx)
	if err != nil {
		return nil, err
	}
	runenv.RecordMessage("Network init complete")

	lat := rand.Intn(latencyMax-latencyMin) + latencyMin

	bw := uint64(bandwidth) * 1000 * 1000

	runenv.RecordMessage("Network params %d %d", lat, bw)

	config := &network.Config{
		Network: "default",
		Enable:  true,
		Default: network.LinkShape{
			Latency:   time.Duration(lat) * time.Millisecond,
			Bandwidth: bw, //Equivalent to 100Mps
		},
		CallbackState: "network-configured",
		RoutingPolicy: network.DenyAll,
	}

	// random delay to avoid overloading weave (we hope)
	delay := time.Duration(rand.Intn(1000)) * time.Millisecond
	<-time.After(delay)
	err = netclient.ConfigureNetwork(ctx, config)
	if err != nil {
		return nil, err
	}

	return config, nil
}

func startDymintNode(runenv *runtime.RunEnv, config *config.NodeConfig, tmConfig *tcfg.Config) (*node.Node, error) {

	/*nodeKey, err := tmp2p.LoadOrGenNodeKey(tmConfig.NodeKeyFile())

	if err != nil {
		return nil, err
	}*/
	privKey := ed25519.GenPrivKey()
	nodeKey := &tmp2p.NodeKey{
		PrivKey: privKey,
	}

	runenv.RecordMessage("nodekey loaded")

	runenv.RecordMessage("privvalkey loaded")

	//genDocProvider := tmnode.DefaultGenesisDocProviderFunc(tmConfig)
	p2pKey, err := conv.GetNodeKey(nodeKey)
	if err != nil {
		return nil, err
	}

	//genesis, err := genDocProvider()
	//runenv.RecordMessage("Genesis file %s", tmConfig.GenesisFile())
	/*genesis, err := types.GenesisDocFromFile(tmConfig.GenesisFile())
	if err != nil {
		return nil, err
	}*/

	genDoc := types.GenesisDoc{
		ChainID:         fmt.Sprintf("test-chain-%v", tmrand.Str(6)),
		GenesisTime:     tmtime.Now(),
		ConsensusParams: types.DefaultConsensusParams(),
	}
	pubKey := privKey.PubKey()
	genDoc.Validators = []types.GenesisValidator{{
		Address: pubKey.Address(),
		PubKey:  pubKey,
		Power:   10,
	}}
	err = conv.GetNodeConfig(config, tmConfig)
	if err != nil {
		return nil, err
	}
	//logger.Info("starting node with ABCI dymint in-process", "conf", config)

	tmConfig.ProxyApp = "kvstore"
	dymintNode, err := node.NewNode(
		context.Background(),
		*config,
		p2pKey,
		p2pKey,
		proxy.DefaultClientCreator(tmConfig.ProxyApp, tmConfig.ABCI, tmConfig.DBDir()),
		&genDoc,
		log.NewTMLogger(log.NewSyncWriter(os.Stdout)),
	)
	if err != nil {
		return nil, err
	}

	return dymintNode, nil
}

func test(runenv *runtime.RunEnv, initCtx *run.InitContext) error {

	params := parseParams(runenv)

	setup := params.setup
	warmup := params.warmup
	cooldown := params.cooldown
	runTime := params.runtime
	totalTime := setup + runTime + warmup + cooldown

	ctx, cancel := context.WithTimeout(context.Background(), totalTime)
	defer cancel()

	runenv.RecordMessage("before sync.MustBoundClient")

	client := tgsync.MustBoundClient(ctx, runenv)
	defer client.Close()

	runenv.RecordMessage("after sync.MustBoundClient")

	//client := initCtx.SyncClient
	//netclient := initCtx.NetClient
	netclient := network.NewClient(client, runenv)

	// Create the hosts, but don't listen yet (we need to set up the data
	// network before listening)

	/*h, err := createHost(ctx, params.netParams.quic)
	if err != nil {
		return err
	}*/

	tmconfig := tcfg.DefaultConfig()
	dymconfig := config.DefaultNodeConfig

	node, err := startDymintNode(runenv, &dymconfig, tmconfig)
	if err != nil {
		return err
	}
	runenv.RecordMessage("Dymint node started %s", node)
	//peers := tgsync.NewTopic("nodes", &peer.AddrInfo{})

	// Get sequence number within a node type (eg honest-1, honest-2, etc)
	// signal entry in the 'enrolled' state, and obtain a sequence number.
	//seq, err := client.Publish(ctx, peers, host.InfoFromHost(h))

	if err != nil {
		return fmt.Errorf("failed to write peer subtree in sync service: %w", err)
	}

	runenv.RecordMessage("before netclient.MustConfigureNetwork")

	_, err = setupNetwork(ctx, runenv, netclient, params.netParams.latency, params.netParams.latencyMax, params.netParams.bandwidthMB)
	if err != nil {
		return fmt.Errorf("Failed to set up network: %w", err)
	}

	netclient.MustWaitNetworkInitialized(ctx)
	//runenv.RecordMessage("my sequence ID: %d %s", seq, h.ID())

	//peerSubscriber := NewPeerSubscriber(ctx, runenv, client, runenv.TestInstanceCount)

	return nil
}
