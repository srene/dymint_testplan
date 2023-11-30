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

	"github.com/testground/sdk-go/runtime"
	tgsync "github.com/testground/sdk-go/sync"
)

type DymintNode struct {
	tmConfig   *tcfg.Config
	config     *config.NodeConfig
	ctx        context.Context
	shutdown   func()
	aggregator bool
	seq        int64
	runenv     *runtime.RunEnv
	node       *node.Node
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

func createDymintNode(ctx context.Context, runenv *runtime.RunEnv, seq int64, client *tgsync.DefaultClient, aggregator bool, ip net.IP) (*DymintNode, error) {

	tmConfig := tcfg.DefaultConfig()
	config := &config.DefaultNodeConfig

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

	tmConfig.ProxyApp = "kvstore"
	tmConfig.LogLevel = "debug"
	config.Aggregator = aggregator

	tmConfig.P2P.ListenAddress = "tcp://" + ip.String() + ":26656"

	runenv.RecordMessage("Listen address %s", tmConfig.P2P.ListenAddress)

	multiaddr := tgsync.NewTopic("addr", &Multiaddr{})

	if !aggregator {
		// Subscribe to the `transfer-key` topic
		tch := make(chan *Multiaddr)
		client.Subscribe(ctx, multiaddr, tch)
		t := <-tch
		tmConfig.P2P.PersistentPeers = t.Addr + "@" + t.Ip + ":" + t.Port
		tmConfig.P2P.Seeds = t.Addr + "@" + t.Ip + ":" + t.Port

		runenv.RecordMessage("Sequencer multiaddr %s", tmConfig.P2P.Seeds)

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
		host, err := libp2p.New(libp2p.Identity(signingKey))
		if err != nil {
			return nil, err
		}
		client.Publish(ctx, multiaddr, &Multiaddr{host.ID().String(), ip.String(), "26656"})

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
	)

	/*server := rpc.NewServer(dymintNode, tmConfig.RPC, logger)
	err = server.Start()
	if err != nil {
		return nil, err
	}*/
	n := &DymintNode{
		tmConfig:   tmConfig,
		config:     config,
		node:       node,
		ctx:        ctx,
		aggregator: aggregator,
		seq:        seq,
		runenv:     runenv,
	}

	return n, nil
}

func (dn *DymintNode) Run(runtime time.Duration) error {
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
