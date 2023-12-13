package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/dymensionxyz/dymint/config"
	"github.com/dymensionxyz/dymint/conv"
	"github.com/dymensionxyz/dymint/node"
	"github.com/dymensionxyz/dymint/rpc"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"

	tcfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/libs/log"
	tmos "github.com/tendermint/tendermint/libs/os"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmnode "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	tmp2p "github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	"github.com/tendermint/tendermint/types"
	tmtime "github.com/tendermint/tendermint/types/time"

	"github.com/testground/sdk-go/runtime"
	tgsync "github.com/testground/sdk-go/sync"
)

type NodeConfig struct {

	// whether we're a publisher or a lurker
	Publisher bool

	FloodPublishing bool

	// pubsub event tracer
	Tracer pubsub.EventTracer

	// Test instance identifier
	Seq int64

	//How long to wait after connecting to bootstrap peers before publishing
	Warmup time.Duration

	// How long to wait for cooldown
	Cooldown time.Duration

	// Gossipsub heartbeat params
	Heartbeat HeartbeatParams

	Failure bool

	FailureDuration time.Duration
	// whether to flood the network when publishing our own messages.
	// Ignored unless hardening_api build tag is present.
	//FloodPublishing bool

	// Params for peer scoring function. Ignored unless hardening_api build tag is present.
	//PeerScoreParams ScoreParams

	OverlayParams OverlayParams

	// Params for inspecting the scoring values.
	//PeerScoreInspect InspectParams

	// Size of the pubsub validation queue.
	ValidateQueueSize int

	// Size of the pubsub outbound queue.
	OutboundQueueSize int

	// Heartbeat tics for opportunistic grafting
	OpportunisticGraftTicks int
}

type DymintNode struct {
	cfg        NodeConfig
	tmConfig   *tcfg.Config
	config     *config.NodeConfig
	ctx        context.Context
	shutdown   func()
	aggregator bool
	seq        int64
	runenv     *runtime.RunEnv
	node       *node.Node
	h          host.Host
	//Tracer     pubsub.EventTracer
}

// InitFilesWithConfig initialises a fresh Dymint instance.
func initFilesWithConfig(ctx context.Context, runenv *runtime.RunEnv, config *tcfg.Config, client *tgsync.DefaultClient, aggregator bool) error {
	// private validator

	config.RootDir = "/"
	dir, _ := os.Getwd()
	os.Mkdir("config", os.ModePerm)
	os.Mkdir("data", os.ModePerm)
	runenv.RecordMessage("Config %s %s %s", dir, config.PrivValidatorKeyFile(), config.GenesisFile())

	privValKeyFile := config.PrivValidatorKeyFile()
	runenv.RecordMessage("Config %s", config.PrivValidatorStateFile())

	privValStateFile := config.PrivValidatorStateFile()
	var pv *privval.FilePV

	pv = privval.GenFilePV("/"+privValKeyFile, "/"+privValStateFile)
	pv.Save()

	runenv.RecordMessage("pv %s", pv.Key.Address)

	nodeKeyFile := config.NodeKeyFile()

	if tmos.FileExists(nodeKeyFile) {
	} else {
		if _, err := p2p.LoadOrGenNodeKey(nodeKeyFile); err != nil {
			return err
		}
	}

	// genesis file
	genFile := config.GenesisFile()

	gen := tgsync.NewTopic("genesis", &Genesis{})
	val := tgsync.NewTopic("validator", &ValidatorKey{})
	if aggregator {
		genDoc := types.GenesisDoc{
			ChainID:         fmt.Sprintf("test-chain-%v", tmrand.Str(6)),
			GenesisTime:     tmtime.Now(),
			ConsensusParams: types.DefaultConsensusParams(),
		}
		pubKey, err := pv.GetPubKey()
		if err != nil {
			return fmt.Errorf("can't get pubkey: %w", err)
		}
		genDoc.Validators = []types.GenesisValidator{{
			Address: pubKey.Address(),
			PubKey:  pubKey,
			Power:   10,
		}}
		if err := genDoc.SaveAs(genFile); err != nil {
			return err
		}
		jsonFile, err := os.Open(genFile)
		byteValue, _ := io.ReadAll(jsonFile)
		_, err = client.Publish(ctx, gen, &Genesis{byteValue})
		runenv.RecordMessage("Genesis doc created for %s", genDoc.ChainID)

		jsonFileVal, err := os.Open(privValKeyFile)
		byteValueVal, _ := io.ReadAll(jsonFileVal)
		_, err = client.Publish(ctx, val, &ValidatorKey{byteValueVal})

	} else {

		tch := make(chan *Genesis)
		client.Subscribe(ctx, gen, tch)
		doc := <-tch
		err := os.WriteFile(genFile, doc.Gen, 0644)
		if err != nil {
			return err
		}
		vch := make(chan *ValidatorKey)
		client.Subscribe(ctx, val, vch)
		key := <-vch
		err = os.WriteFile(privValKeyFile, key.ValKey, 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

func createDymintNode(ctx context.Context, runenv *runtime.RunEnv, seq int64, client *tgsync.DefaultClient, aggregator bool, ip net.IP, cfg NodeConfig) (*DymintNode, error) {

	opts, err := pubsubOptions(cfg)
	// Set the heartbeat initial delay and interval
	pubsub.GossipSubHeartbeatInitialDelay = cfg.Heartbeat.InitialDelay
	pubsub.GossipSubHeartbeatInterval = cfg.Heartbeat.Interval
	pubsub.GossipSubHistoryLength = 100
	pubsub.GossipSubHistoryGossip = 50

	tmConfig := tcfg.DefaultConfig()
	config := &config.DefaultNodeConfig

	config.SettlementConfig.KeyringHomeDir = "/"
	config.DALayer = "grpc"
	config.SettlementLayer = "grpc"
	initFilesWithConfig(ctx, runenv, tmConfig, client, aggregator)

	nodeKey, err := tmp2p.LoadOrGenNodeKey(tmConfig.NodeKeyFile())
	if err != nil {
		return nil, err
	}
	privValKey, err := tmp2p.LoadOrGenNodeKey(tmConfig.PrivValidatorKeyFile())
	if err != nil {
		return nil, err
	}
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
	runenv.RecordMessage("Genesis chain %s", genesis.ChainID)

	config.BatchSubmitMaxTime = time.Hour
	config.BlockBatchMaxSizeBytes = 50000000

	config.DALayer = "grpc"
	tmConfig.ProxyApp = "kvstore"
	tmConfig.LogLevel = "debug"
	config.Aggregator = aggregator
	tmConfig.P2P.ListenAddress = "tcp://" + ip.String() + ":26656"
	tmConfig.RPC.ListenAddress = "tcp://" + ip.String() + ":26657"
	runenv.RecordMessage("Listen address %s", tmConfig.P2P.ListenAddress)

	multiaddr := tgsync.NewTopic("addr", &Multiaddr{})

	var host host.Host

	var kv int
	kvstore := tgsync.NewTopic("kv", &KV{})

	if !aggregator {
		// Subscribe to the `transfer-key` topic
		tch := make(chan *Multiaddr)
		client.Subscribe(ctx, multiaddr, tch)
		t := <-tch
		tmConfig.P2P.PersistentPeers = t.Addr + "@" + t.Ip + ":" + t.Port
		tmConfig.P2P.Seeds = t.Addr + "@" + t.Ip + ":" + t.Port

		runenv.RecordMessage("Sequencer multiaddr %s", tmConfig.P2P.Seeds)
		nodeKey, err := p2p.LoadNodeKey(tmConfig.NodeKeyFile())
		if err != nil {
			return nil, err
		}
		signingKey, err := conv.GetNodeKey(nodeKey)
		if err != nil {
			return nil, err
		}
		// convert nodeKey to libp2p key
		host, err = libp2p.New(libp2p.Identity(signingKey))

		kch := make(chan *KV)
		client.Subscribe(ctx, kvstore, kch)
		runenv.RecordMessage("Waiting for SL KVstore")
		store := <-kch
		kv = store.Kv
		runenv.RecordMessage("KV received %d", kv)

	} else {
		nodeKey, err := p2p.LoadNodeKey(tmConfig.NodeKeyFile())
		if err != nil {
			return nil, err
		}
		signingKey, err := conv.GetNodeKey(nodeKey)
		if err != nil {
			return nil, err
		}
		// convert nodeKey to libp2p key
		host, err = libp2p.New(libp2p.Identity(signingKey))
		if err != nil {
			return nil, err
		}
		client.Publish(ctx, multiaddr, &Multiaddr{host.ID().String(), ip.String(), "26656"})

		//kv = store.NewDefaultInMemoryKVStore()

		client.Publish(ctx, kvstore, &KV{0})

	}

	err = conv.GetNodeConfig(config, tmConfig)
	if err != nil {
		return nil, err
	}
	runenv.RecordMessage("starting node with ABCI dymint with ip %s", ip)

	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))

	node, err := node.NewNode(
		context.Background(),
		*config,
		p2pKey,
		signingKey,
		proxy.DefaultClientCreator(tmConfig.ProxyApp, tmConfig.ABCI, tmConfig.DBDir()),
		genesis,
		logger,
		nil,
		opts...,
	)

	server := rpc.NewServer(node, tmConfig.RPC, logger)
	err = server.Start()
	if err != nil {
		return nil, err
	}
	n := &DymintNode{
		cfg:        cfg,
		tmConfig:   tmConfig,
		config:     config,
		node:       node,
		ctx:        ctx,
		aggregator: aggregator,
		seq:        seq,
		runenv:     runenv,
		h:          host,
	}

	return n, nil
}

func pubsubOptions(cfg NodeConfig) ([]pubsub.Option, error) {
	opts := []pubsub.Option{
		pubsub.WithEventTracer(cfg.Tracer),
	}

	if cfg.ValidateQueueSize > 0 {
		opts = append(opts, pubsub.WithValidateQueueSize(cfg.ValidateQueueSize))
	}

	if cfg.OutboundQueueSize > 0 {
		opts = append(opts, pubsub.WithPeerOutboundQueueSize(cfg.OutboundQueueSize))
	}

	// Set the overlay parameters
	if cfg.OverlayParams.d >= 0 {
		pubsub.GossipSubD = cfg.OverlayParams.d
	}
	if cfg.OverlayParams.dlo >= 0 {
		pubsub.GossipSubDlo = cfg.OverlayParams.dlo
	}
	if cfg.OverlayParams.dhi >= 0 {
		pubsub.GossipSubDhi = cfg.OverlayParams.dhi
	}

	return opts, nil
}

func (dn *DymintNode) Run(runtime time.Duration, cfg NodeConfig) error {

	//opts, err := pubsubOptions(cfg)

	defer func() {
		dn.runenv.RecordMessage("Shutting down")
		dn.node.Stop()
	}()
	dn.node.Start()

	select {
	case <-time.After(runtime):
	case <-dn.ctx.Done():
		return dn.ctx.Err()
	}

	return nil
}
