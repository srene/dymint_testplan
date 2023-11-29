package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
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

	"github.com/testground/sdk-go/network"
	"github.com/testground/sdk-go/run"
	"github.com/testground/sdk-go/runtime"
	tgsync "github.com/testground/sdk-go/sync"

	"github.com/dymensionxyz/dymint/config"
	"github.com/dymensionxyz/dymint/conv"
	"github.com/dymensionxyz/dymint/node"
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

// InitFilesWithConfig initialises a fresh Dymint instance.
func InitFilesWithConfig(ctx context.Context, runenv *runtime.RunEnv, config *tcfg.Config, client *tgsync.DefaultClient, aggregator bool) error {
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

	if aggregator == true {
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

	} else {

		tch := make(chan *Genesis)
		client.Subscribe(ctx, gen, tch)
		doc := <-tch
		err := os.WriteFile(genFile, doc.Gen, 0644)
		if err != nil {
			return err
		}

	}

	return nil
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

func createDymintNode(ctx context.Context, runenv *runtime.RunEnv, client *tgsync.DefaultClient, config *config.NodeConfig, tmConfig *tcfg.Config, aggregator bool, ip net.IP) (*node.Node, error) {

	InitFilesWithConfig(ctx, runenv, tmConfig, client, aggregator)

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
	//logger.Info("starting node with ABCI dymint in-process", "conf", config)

	config.BatchSubmitMaxTime = time.Hour
	config.BlockBatchMaxSizeBytes = 50000000

	tmConfig.ProxyApp = "kvstore"
	tmConfig.LogLevel = "debug"
	//tmConfig.DBPath = "/"
	config.Aggregator = aggregator

	tmConfig.P2P.ListenAddress = "tcp://" + ip.String() + ":26656"

	runenv.RecordMessage("Listen address %s", tmConfig.P2P.ListenAddress)

	multiaddr := tgsync.NewTopic("addr", &Multiaddr{})

	if aggregator == false {
		// Subscribe to the `transfer-key` topic
		tch := make(chan *Multiaddr)
		client.Subscribe(ctx, multiaddr, tch)
		t := <-tch
		tmConfig.P2P.PersistentPeers = t.Addr + "@" + t.Ip + ":" + t.Port
		tmConfig.P2P.Seeds = t.Addr + "@" + t.Ip + ":" + t.Port

		runenv.RecordMessage("Sequencer multiaddr %s", tmConfig.P2P.Seeds)

	}

	err = conv.GetNodeConfig(config, tmConfig)
	if err != nil {
		return nil, err
	}
	runenv.RecordMessage("starting node with ABCI dymint with ip %s", ip)

	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	dymintNode, err := node.NewNode(
		context.Background(),
		*config,
		p2pKey,
		signingKey,
		proxy.DefaultClientCreator(tmConfig.ProxyApp, tmConfig.ABCI, tmConfig.DBDir()),
		genesis,
		logger,
	)

	/*server := rpc.NewServer(dymintNode, tmConfig.RPC, logger)
	err = server.Start()
	if err != nil {
		return nil, err
	}*/
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

	client := tgsync.MustBoundClient(ctx, runenv)
	defer client.Close()

	netclient := network.NewClient(client, runenv)

	_, err := setupNetwork(ctx, runenv, netclient, params.netParams.latency, params.netParams.latencyMax, params.netParams.bandwidthMB)
	if err != nil {
		return fmt.Errorf("Failed to set up network: %w", err)
	}

	netclient.MustWaitNetworkInitialized(ctx)
	//runenv.RecordMessage("my sequence ID: %d %s", seq, h.ID())
	tmconfig := tcfg.DefaultConfig()
	dymconfig := config.DefaultNodeConfig

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

	node, err := createDymintNode(ctx, runenv, client, &dymconfig, tmconfig, aggregator, ip)
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
	multiaddr := tgsync.NewTopic("addr", &Multiaddr{})
	_, address, _ := node.P2P.Info()
	privValKey, err := tmp2p.LoadOrGenNodeKey(tmconfig.PrivValidatorKeyFile())
	signingKey, err := conv.GetNodeKey(privValKey)
	host, err := libp2p.New(libp2p.Identity(signingKey))
	if err != nil {
		return err
	}

	addr := strings.Split(address, "/")
	//runenv.RecordMessage("Sequencer address %s %s %s %s", host.ID(), addr[2], addr[4], address)
	_, err = client.Publish(ctx, multiaddr, &Multiaddr{host.ID().String(), addr[2], addr[4]})
	if err != nil {
		return err
	}
	//peerSubscriber := NewPeerSubscriber(ctx, runenv, client, runenv.TestInstanceCount)
	time.Sleep(30 * time.Second)
	return nil
}
