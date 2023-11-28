package main

import (
	"context"
	"fmt"

	tgsync "github.com/testground/sdk-go/sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/testground/sdk-go/network"
	"github.com/testground/sdk-go/run"
	"github.com/testground/sdk-go/runtime"
)

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

	peers := tgsync.NewTopic("nodes", &peer.AddrInfo{})

	// Get sequence number within a node type (eg honest-1, honest-2, etc)
	// signal entry in the 'enrolled' state, and obtain a sequence number.
	seq, err := client.Publish(ctx, peers, host.InfoFromHost(h))

	if err != nil {
		return fmt.Errorf("failed to write peer subtree in sync service: %w", err)
	}

	runenv.RecordMessage("before netclient.MustConfigureNetwork")

	//config, err := setupNetwork(ctx, runenv, netclient, params.netParams.latency, params.netParams.latencyMax, params.netParams.bandwidthMB)
	if err != nil {
		return fmt.Errorf("Failed to set up network: %w", err)
	}

	netclient.MustWaitNetworkInitialized(ctx)
	runenv.RecordMessage("my sequence ID: %d %s", seq, h.ID())

	//peerSubscriber := NewPeerSubscriber(ctx, runenv, client, runenv.TestInstanceCount)

	return nil
}
