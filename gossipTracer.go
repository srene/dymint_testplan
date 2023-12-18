package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	pb "github.com/libp2p/go-libp2p-pubsub/pb"
)

type RPCMetrics struct {
	RPCs     uint64
	Messages uint64
	Grafts   uint64
	Prunes   uint64
	IWants   uint64
	IHaves   uint64
}

type GossipTestMetrics struct {
	LocalPeer    string
	Published    uint64
	Rejected     uint64
	Delivered    uint64
	Duplicates   uint64
	DroppedRPC   uint64
	PeersAdded   uint64
	PeersRemoved uint64
	TopicsJoined uint64
	TopicsLeft   uint64

	SentRPC     RPCMetrics
	ReceivedRPC RPCMetrics
}

type GossipTestTracer struct {
	full                pubsub.EventTracer
	filtered            pubsub.EventTracer
	aggregateOutputPath string

	eventCh chan *pb.TraceEvent
	doneCh  chan struct{}

	metrics GossipTestMetrics
}

func NewGossipTestTracer(outputPathPrefix string, localPeerID string, full bool) (*GossipTestTracer, error) {
	var fullTracer pubsub.EventTracer
	var err error
	if full {
		fullTracer, err = pubsub.NewPBTracer(outputPathPrefix + "-full.bin")
		if err != nil {
			return nil, fmt.Errorf("error making protobuf event tracer: %s", err)
		}
	}

	filteredTracer, err := newFilteringTracer(outputPathPrefix+"-filtered.bin",
		pb.TraceEvent_PUBLISH_MESSAGE, pb.TraceEvent_DELIVER_MESSAGE,
		pb.TraceEvent_GRAFT, pb.TraceEvent_PRUNE)
	if err != nil {
		return nil, fmt.Errorf("error making filtered event tracer: %s", err)
	}

	t := &GossipTestTracer{
		full:                fullTracer,
		filtered:            filteredTracer,
		aggregateOutputPath: outputPathPrefix + "-aggregate.json",
		eventCh:             make(chan *pb.TraceEvent, 1024),
		doneCh:              make(chan struct{}, 1),
	}

	t.metrics.LocalPeer = localPeerID

	go t.eventLoop()
	return t, nil
}

func (t *GossipTestTracer) Stop() error {
	t.doneCh <- struct{}{}

	jsonstr, err := json.MarshalIndent(t.metrics, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(t.aggregateOutputPath, jsonstr, os.ModePerm)
}

func (t *GossipTestTracer) eventLoop() {
	for {
		select {
		case <-t.doneCh:
			return
		case evt := <-t.eventCh:
			switch evt.GetType() {
			case pb.TraceEvent_PUBLISH_MESSAGE:
				t.publishMessage(evt)
			case pb.TraceEvent_REJECT_MESSAGE:
				t.rejectMessage(evt)
			case pb.TraceEvent_DUPLICATE_MESSAGE:
				t.duplicateMessage(evt)
			case pb.TraceEvent_DELIVER_MESSAGE:
				t.deliverMessage(evt)
			case pb.TraceEvent_ADD_PEER:
				t.addPeer(evt)
			case pb.TraceEvent_REMOVE_PEER:
				t.removePeer(evt)
			case pb.TraceEvent_RECV_RPC:
				t.recvRPC(evt)
			case pb.TraceEvent_SEND_RPC:
				t.sendRPC(evt)
			case pb.TraceEvent_DROP_RPC:
				t.dropRPC(evt)
			case pb.TraceEvent_JOIN:
				t.join(evt)
			case pb.TraceEvent_LEAVE:
				t.leave(evt)
			case pb.TraceEvent_GRAFT:
				t.graft(evt)
			case pb.TraceEvent_PRUNE:
				t.prune(evt)
			}
		}
	}
}

func (t *GossipTestTracer) Trace(evt *pb.TraceEvent) {
	t.filtered.Trace(evt)
	if t.full != nil {
		t.full.Trace(evt)
	}
	t.eventCh <- evt
}

func (t *GossipTestTracer) publishMessage(evt *pb.TraceEvent) {
	t.metrics.Published++
}

func (t *GossipTestTracer) rejectMessage(evt *pb.TraceEvent) {
	t.metrics.Rejected++
}

func (t *GossipTestTracer) deliverMessage(evt *pb.TraceEvent) {
	t.metrics.Delivered++
}

func (t *GossipTestTracer) duplicateMessage(evt *pb.TraceEvent) {
	t.metrics.Duplicates++
}

func (t *GossipTestTracer) sendRPC(evt *pb.TraceEvent) {
	meta := evt.GetSendRPC().GetMeta()
	updateRPCStats(&t.metrics.SentRPC, meta)
}

func (t *GossipTestTracer) recvRPC(evt *pb.TraceEvent) {
	meta := evt.GetRecvRPC().GetMeta()
	updateRPCStats(&t.metrics.ReceivedRPC, meta)
}

func updateRPCStats(stats *RPCMetrics, meta *pb.TraceEvent_RPCMeta) {
	ctrl := meta.GetControl()
	stats.RPCs += 1
	stats.Messages += uint64(len(meta.GetMessages()))
	stats.IHaves += uint64(len(ctrl.GetIhave()))
	stats.IWants += uint64(len(ctrl.GetIwant()))
	stats.Grafts += uint64(len(ctrl.GetGraft()))
	stats.Prunes += uint64(len(ctrl.GetPrune()))
}

func (t *GossipTestTracer) dropRPC(evt *pb.TraceEvent) {
	t.metrics.DroppedRPC++
}

func (t *GossipTestTracer) addPeer(evt *pb.TraceEvent) {
	t.metrics.PeersAdded++
}

func (t *GossipTestTracer) removePeer(evt *pb.TraceEvent) {
	t.metrics.PeersRemoved++
}

func (t *GossipTestTracer) join(evt *pb.TraceEvent) {
	t.metrics.TopicsJoined++
}

func (t *GossipTestTracer) leave(evt *pb.TraceEvent) {
	t.metrics.TopicsLeft++
}

func (t *GossipTestTracer) graft(evt *pb.TraceEvent) {
	// already accounted for in sendRPC
}

func (t *GossipTestTracer) prune(evt *pb.TraceEvent) {
	// already accounted for in sendRPC
}

var _ pubsub.EventTracer = (*GossipTestTracer)(nil)

type filteringTracer struct {
	pubsub.EventTracer
	whitelist []pb.TraceEvent_Type
}

func newFilteringTracer(outputPath string, typeWhitelist ...pb.TraceEvent_Type) (*filteringTracer, error) {
	tracer, err := pubsub.NewPBTracer(outputPath)
	if err != nil {
		return nil, fmt.Errorf("error making protobuf event tracer: %s", err)
	}
	return &filteringTracer{EventTracer: tracer, whitelist: typeWhitelist}, nil
}

func (t *filteringTracer) Trace(evt *pb.TraceEvent) {
	for _, typ := range t.whitelist {
		if evt.GetType() == typ {
			t.EventTracer.Trace(evt)
			return
		}
	}
}
