package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/testground/sdk-go/network"
	"github.com/testground/sdk-go/run"
	"github.com/testground/sdk-go/runtime"
	tgsync "github.com/testground/sdk-go/sync"
)

type IP struct {
	Address net.IP
}

type Multiaddr struct {
	Addr string
	Ip   string
	Port string
}

type Genesis struct {
	Gen []byte
}

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

func test(runenv *runtime.RunEnv, initCtx *run.InitContext) error {

	params := parseParams(runenv)

	setup := params.setup
	warmup := params.warmup
	cooldown := params.cooldown
	runTime := params.runtime
	totalTime := setup + runTime + warmup + cooldown

	ctx, cancel := context.WithTimeout(context.Background(), totalTime)
	defer cancel()

	client := tgsync.MustBoundClient(ctx, runenv)
	defer client.Close()

	netclient := network.NewClient(client, runenv)

	_, err := setupNetwork(ctx, runenv, netclient, params.netParams.latency, params.netParams.latencyMax, params.netParams.bandwidthMB)
	if err != nil {
		return fmt.Errorf("Failed to set up network: %w", err)
	}

	netclient.MustWaitNetworkInitialized(ctx)
	//runenv.RecordMessage("my sequence ID: %d %s", seq, h.ID())

	peers := tgsync.NewTopic("nodes", &IP{})

	// Get sequence number within a node type (eg honest-1, honest-2, etc)
	// signal entry in the 'enrolled' state, and obtain a sequence number.
	ip, _ := netclient.GetDataNetworkIP()
	seq, err := client.Publish(ctx, peers, &IP{ip})

	var aggregator bool
	if seq == 1 {
		aggregator = true
	} else {
		aggregator = false
	}
	runenv.RecordMessage("initialization: dymint node seq %d", seq)
	if err != nil {
		return fmt.Errorf("failed to write peer subtree in sync service: %w", err)
	}

	ip, err = netclient.GetDataNetworkIP()

	node, err := createDymintNode(ctx, runenv, client, aggregator, ip)
	if err != nil {
		return err
	}

	runenv.RecordMessage("initialization: dymint node created")
	if err := node.Start(); err != nil {
		return err
	}
	runenv.RecordMessage("Node started at %s", node.P2P.Addrs())
	if err != nil {
		return err
	}

	//peerSubscriber := NewPeerSubscriber(ctx, runenv, client, runenv.TestInstanceCount)
	time.Sleep(30 * time.Second)
	return nil
}
