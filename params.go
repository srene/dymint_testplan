package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/testground/sdk-go/ptypes"
	"github.com/testground/sdk-go/runtime"
)

type TopicConfig struct {
	Id          string
	MessageRate ptypes.Rate
	MessageSize ptypes.Size
}

type HeartbeatParams struct {
	InitialDelay time.Duration
	Interval     time.Duration
}

type NetworkParams struct {
	latency     int
	latencyMax  int
	jitterPct   int
	bandwidthMB int
	quic        bool
}

type OverlayParams struct {
	d            int
	dlo          int
	dhi          int
	dscore       int
	dlazy        int
	dout         int
	gossipFactor float64
}

type testParams struct {
	heartbeat HeartbeatParams
	setup     time.Duration
	warmup    time.Duration
	runtime   time.Duration
	cooldown  time.Duration

	publisher         bool
	floodPublishing   bool
	fullTraces        bool
	topics            []TopicConfig
	degree            int
	node_failing      int
	node_failure_time time.Duration

	containerNodesTotal int
	nodesPerContainer   int

	connectDelays           []time.Duration
	connectDelayJitterPct   int
	attackSingleNode        bool
	censorSingleNode        bool
	connectToPublishersOnly bool

	netParams          NetworkParams
	overlayParams      OverlayParams
	scoreInspectPeriod time.Duration
	validateQueueSize  int
	outboundQueueSize  int

	opportunisticGraftTicks int

	block_size    int
	blocks_second int
}

func durationParam(runenv *runtime.RunEnv, name string) time.Duration {
	if !runenv.IsParamSet(name) {
		runenv.RecordMessage("duration param %s not set, defaulting to zero", name)
		return 0
	}
	return parseDuration(runenv.StringParam(name))
}

func parseDuration(val string) time.Duration {
	// FIXME: this seems like a testground bug... when default string params are not
	// overridden by the command line, the value is wrapped in double quote chars,
	// e.g. `"2m"` instead of  `2m`
	s := strings.ReplaceAll(val, "\"", "")
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(fmt.Errorf("param %s is not a valid duration: %s", val, err))
	}
	return d
}

func parseParams(runenv *runtime.RunEnv) testParams {

	np := NetworkParams{
		latency:     runenv.IntParam("t_latency"),
		latencyMax:  runenv.IntParam("t_latency_max"),
		jitterPct:   runenv.IntParam("jitter_pct"),
		bandwidthMB: runenv.IntParam("bandwidth_mb"),
		quic:        runenv.BooleanParam("quic"),
	}

	op := OverlayParams{
		d:            runenv.IntParam("overlay_d"),
		dlo:          runenv.IntParam("overlay_dlo"),
		dhi:          runenv.IntParam("overlay_dhi"),
		dscore:       runenv.IntParam("overlay_dscore"),
		dlazy:        runenv.IntParam("overlay_dlazy"),
		dout:         runenv.IntParam("overlay_dout"),
		gossipFactor: runenv.FloatParam("gossip_factor"),
	}

	p := testParams{
		heartbeat: HeartbeatParams{
			InitialDelay: durationParam(runenv, "t_heartbeat_initial_delay"),
			Interval:     durationParam(runenv, "t_heartbeat"),
		},
		setup:           durationParam(runenv, "t_setup"),
		warmup:          durationParam(runenv, "t_warm"),
		runtime:         durationParam(runenv, "t_run"),
		cooldown:        durationParam(runenv, "t_cool"),
		publisher:       runenv.BooleanParam("publisher"),
		floodPublishing: runenv.BooleanParam("flood_publishing"),
		fullTraces:      runenv.BooleanParam("full_traces"),
		//nodeType:                parseNodeType(runenv.StringParam("attack_node_type")),
		attackSingleNode:        runenv.BooleanParam("attack_single_node"),
		censorSingleNode:        runenv.BooleanParam("censor_single_node"),
		connectToPublishersOnly: runenv.BooleanParam("connect_to_publishers_only"),
		degree:                  runenv.IntParam("degree"),
		node_failing:            runenv.IntParam("node_failing"),
		node_failure_time:       durationParam(runenv, "t_node_failure"),
		containerNodesTotal:     runenv.IntParam("n_container_nodes_total"),
		nodesPerContainer:       runenv.IntParam("n_nodes_per_container"),
		scoreInspectPeriod:      durationParam(runenv, "t_score_inspect_period"),
		netParams:               np,
		overlayParams:           op,
		validateQueueSize:       runenv.IntParam("validate_queue_size"),
		outboundQueueSize:       runenv.IntParam("outbound_queue_size"),
		opportunisticGraftTicks: runenv.IntParam("opportunistic_graft_ticks"),
		block_size:              runenv.IntParam("block_size"),
		blocks_second:           runenv.IntParam("blocks_second"),
	}

	if runenv.IsParamSet("topics") {
		jsonstr := runenv.StringParam("topics")
		err := json.Unmarshal([]byte(jsonstr), &p.topics)
		if err != nil {
			panic(err)
		}
		runenv.RecordMessage("topics: %v", p.topics)
	}

	if runenv.IsParamSet("connect_delays") {
		// eg: "5@10s,15@1m,5@2m"
		connDelays := runenv.StringParam("connect_delays")
		if connDelays != "" && connDelays != "\"\"" {
			cds := strings.Split(connDelays, ",")
			for _, cd := range cds {
				parts := strings.Split(cd, "@")
				if len(parts) != 2 {
					panic(fmt.Sprintf("Badly formatted connect_delays param %s", connDelays))
				}
				count, err := strconv.Atoi(parts[0])
				if err != nil {
					panic(fmt.Sprintf("Badly formatted connect_delays param %s", connDelays))
				}

				dur := parseDuration(parts[1])
				for i := 0; i < count; i++ {
					p.connectDelays = append(p.connectDelays, dur)
				}
			}
		}

		p.connectDelayJitterPct = 5
		if runenv.IsParamSet("connect_delay_jitter_pct") {
			p.connectDelayJitterPct = runenv.IntParam("connect_delay_jitter_pct")
		}
	}

	return p
}
