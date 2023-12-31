package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/testground/sdk-go/network"
	"github.com/testground/sdk-go/run"
	"github.com/testground/sdk-go/runtime"
	tgsync "github.com/testground/sdk-go/sync"
	"golang.org/x/sync/errgroup"

	"github.com/srene/tm-load-test/pkg/loadtest"
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

type ValidatorKey struct {
	ValKey []byte
}

type KV struct {
	Kv int
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
	//delay := time.Duration(rand.Intn(1000)) * time.Millisecond
	//<-time.After(delay)
	err = netclient.ConfigureNetwork(ctx, config)
	if err != nil {
		return nil, err
	}

	return config, nil
}

func sendingTransactions(ctx context.Context, runenv *runtime.RunEnv, warmup time.Duration, runTime time.Duration, ip net.IP) {

	var cfg loadtest.Config
	cfg.ClientFactory = "kvstore"
	cfg.Endpoints = []string{"ws://" + ip.String() + ":26657/websocket"}
	cfg.Connections = 1
	cfg.Count = -1
	cfg.BroadcastTxMethod = "sync"
	cfg.Rate = 50
	cfg.SendPeriod = 0.1
	cfg.Size = 1000
	cfg.Time = int(runTime.Seconds())
	cfg.EndpointSelectMethod = "any"
	runenv.RecordMessage("Connecting to remote endpoints ", cfg.Endpoints, cfg.MaxTxsPerEndpoint(), cfg.Rate, cfg.Time, cfg.Count)

	if err := cfg.Validate(); err != nil {
		runenv.RecordMessage(err.Error())
		//os.Exit(1)
	}
	tg := loadtest.NewTransactorGroup()
	//tg.SetLogger(logger)
	if err := tg.AddAll(&cfg); err != nil {
		runenv.RecordCrash("adding transactor error")
		return
	}

	select {
	case <-time.After(warmup):
	case <-ctx.Done():
		return
	}
	runenv.RecordMessage("Initiating load test %s", cfg.Endpoints)
	tg.Start()

	if err := tg.Wait(); err != nil {
		runenv.RecordMessage("Failed to execute load test", "err", err)
		return
	}

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

	test := tgsync.NewTopic("test", &IP{})
	seq, err := client.Publish(ctx, test, &IP{})
	runenv.RecordMessage("my sequence ID: %d ", seq)

	bw := params.netParams.bandwidthMB
	//if int(seq) == runenv.TestInstanceCount-1 || int(seq) == runenv.TestInstanceCount {
	if int(seq) != 2 {
		bw = bw * 1000
	}
	_, err = setupNetwork(ctx, runenv, netclient, params.netParams.latency, params.netParams.latencyMax, bw)
	if err != nil {
		return fmt.Errorf("Failed to set up network: %w", err)
	}

	netclient.MustWaitNetworkInitialized(ctx)
	//runenv.RecordMessage("my sequence ID: %d %s", seq, h.ID())

	peers := tgsync.NewTopic("nodes", &IP{})

	// Get sequence number within a node type (eg honest-1, honest-2, etc)
	// signal entry in the 'enrolled' state, and obtain a sequence number.
	ip, _ := netclient.GetDataNetworkIP()
	_, err = client.Publish(ctx, peers, &IP{ip})
	//runenv.RecordMessage("my sequence ID: %d ", seq)

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

	tracerOut := fmt.Sprintf("%s%ctracer-output-%d", runenv.TestOutputsPath, os.PathSeparator, seq)
	tracer, err := NewTestTracer(tracerOut, fmt.Sprint(seq), true)

	gossipTracerOut := fmt.Sprintf("%s%cgossiptracer-output-%d", runenv.TestOutputsPath, os.PathSeparator, seq)
	gossipTracer, err := NewGossipTestTracer(gossipTracerOut, fmt.Sprint(seq), true)

	nodeFailing := false

	if seq == int64(params.node_failing) {
		nodeFailing = true
		runenv.RecordMessage("Enabling failure for node %d !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!", seq)
	}

	daAddr := tgsync.NewTopic("da_addr", &Multiaddr{})
	slAddr := tgsync.NewTopic("sl_addr", &Multiaddr{})

	errgrp, _ := errgroup.WithContext(ctx)

	if int(seq) == runenv.TestInstanceCount {
		client.Publish(ctx, slAddr, &Multiaddr{"", ip.String(), "7980"})
		//errgrp, _ := errgroup.WithContext(ctx)
		errgrp.Go(func() (err error) {
			runGrpcSlServer(runTime, ip.String(), 7980)
			return
		})
		//go runGrpcSlServer(runTime, ip.String(), 7981)
		//go runGrpcDaServer(runTime, ip.String(), 7980)
		//return errgrp.Wait()
	} else if int(seq) == runenv.TestInstanceCount-1 {
		client.Publish(ctx, daAddr, &Multiaddr{"", ip.String(), "7981"})
		//errgrp, _ := errgroup.WithContext(ctx)
		errgrp.Go(func() (err error) {
			runGrpcDaServer(runTime, ip.String(), 7981)
			return
		})
		//go runGrpcSlServer(runTime, ip.String(), 7981)
		//go runGrpcDaServer(runTime, ip.String(), 7980)
		//return errgrp.Wait()
	} else {
		sch := make(chan *Multiaddr)
		client.Subscribe(ctx, slAddr, sch)
		s := <-sch
		dch := make(chan *Multiaddr)
		client.Subscribe(ctx, daAddr, dch)
		d := <-dch
		cfg := NodeConfig{
			Publisher:               aggregator,
			FloodPublishing:         false,
			P2p:                     params.p2p,
			Grpc:                    params.grpc,
			OverlayParams:           params.overlayParams,
			FailureDuration:         params.node_failure_time,
			Failure:                 nodeFailing,
			Tracer:                  tracer,
			GossipTracer:            gossipTracer,
			Seq:                     seq,
			Warmup:                  params.warmup,
			Cooldown:                params.cooldown,
			Heartbeat:               params.heartbeat,
			ValidateQueueSize:       params.validateQueueSize,
			OutboundQueueSize:       params.outboundQueueSize,
			OpportunisticGraftTicks: params.opportunisticGraftTicks,
		}

		dn, err := createDymintNode(ctx, runenv, seq, client, aggregator, ip, cfg, d.Ip, s.Ip, d.Port, s.Port)
		if err != nil {
			return err
		}

		runenv.RecordMessage("Node started %d", dn.seq)

		errgrp.Go(func() (err error) {
			dn.Run(runTime+warmup, cfg)
			return
		})
		if seq == 1 {
			sendingTransactions(ctx, runenv, warmup+5*time.Second, 5*time.Second, ip)
		}
	}
	return errgrp.Wait()

}
