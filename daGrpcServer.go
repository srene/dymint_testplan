package main

import (
	"log"
	"net"
	"strconv"

	grpcda "github.com/dymensionxyz/dymint/da/grpc"
	"github.com/dymensionxyz/dymint/da/grpc/mockserv"
	"github.com/dymensionxyz/dymint/store"
)

func runGrpcDaServer(ip string, port int) {
	conf := grpcda.DefaultConfig

	conf.Host = ip
	conf.Port = port
	kv := store.NewDefaultKVStore(".", "db", "dymint")
	lis, err := net.Listen("tcp", conf.Host+":"+strconv.Itoa(conf.Port))
	if err != nil {
		log.Panic(err)
	}
	log.Println("DA Grpc Server Listening on:", lis.Addr())
	srv := mockserv.GetServer(kv, conf, nil)
	if err := srv.Serve(lis); err != nil {
		log.Println("DA error while serving:", err)
	}
}
