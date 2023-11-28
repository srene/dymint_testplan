package main

import (
	"context"
	"fmt"

	tcfg "github.com/tendermint/tendermint/config"

	"github.com/tendermint/tendermint/crypto"
	tmnode "github.com/tendermint/tendermint/node"
	tmp2p "github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/proxy"

	tgsync "github.com/testground/sdk-go/sync"

	"github.com/testground/sdk-go/network"
	"github.com/testground/sdk-go/run"
	"github.com/testground/sdk-go/runtime"

	"github.com/dymensionxyz/dymint/config"
	"github.com/dymensionxyz/dymint/conv"
	"github.com/dymensionxyz/dymint/node"
)

type NodeKey struct {
	PrivKey crypto.PrivKey `json:"priv_key"` // our priv key
}

func startDymintNode(config *config.NodeConfig, tmConfig *tcfg.Config) (*node.Node, error) {
	nodeKey, err := tmp2p.LoadOrGenNodeKey(tmConfig.NodeKeyFile())
	if err != nil {
		return nil, err
	}
	privValKey, err := tmp2p.LoadOrGenNodeKey(tmConfig.PrivValidatorKeyFile())

	genDocProvider := tmnode.DefaultGenesisDocProviderFunc(tmConfig)
	p2pKey, err := conv.GetNodeKey(nodeKey)
	if err != nil {
		return nil, err
	}
	signingKey, err := conv.GetNodeKey(privValKey)
	if err != nil {
		return nil, err
	}
	genesis, err := genDocProvider()
	if err != nil {
		return nil, err
	}
	err = conv.GetNodeConfig(config, tmConfig)
	if err != nil {
		return nil, err
	}
	//logger.Info("starting node with ABCI dymint in-process", "conf", config)

	dymintNode, err := node.NewNode(
		context.Background(),
		*config,
		p2pKey,
		signingKey,
		proxy.DefaultClientCreator(tmConfig.ProxyApp, tmConfig.ABCI, tmConfig.DBDir()),
		genesis,
		nil,
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

	node, err := startDymintNode(&dymconfig, tmconfig)
	if err != nil {
		return err
	}
	runenv.RecordMessage("Dymint node started %s", node.P2P.Addrs())
	//peers := tgsync.NewTopic("nodes", &peer.AddrInfo{})

	// Get sequence number within a node type (eg honest-1, honest-2, etc)
	// signal entry in the 'enrolled' state, and obtain a sequence number.
	//seq, err := client.Publish(ctx, peers, host.InfoFromHost(h))

	if err != nil {
		return fmt.Errorf("failed to write peer subtree in sync service: %w", err)
	}

	runenv.RecordMessage("before netclient.MustConfigureNetwork")

	//config, err := setupNetwork(ctx, runenv, netclient, params.netParams.latency, params.netParams.latencyMax, params.netParams.bandwidthMB)
	if err != nil {
		return fmt.Errorf("Failed to set up network: %w", err)
	}

	netclient.MustWaitNetworkInitialized(ctx)
	//runenv.RecordMessage("my sequence ID: %d %s", seq, h.ID())

	//peerSubscriber := NewPeerSubscriber(ctx, runenv, client, runenv.TestInstanceCount)

	return nil
}
