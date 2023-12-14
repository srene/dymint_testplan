package main

import (
	"log"
	"net"
	"strconv"

	"github.com/dymensionxyz/dymint/settlement"
	"github.com/dymensionxyz/dymint/settlement/grpc/mockserv"
)

func runGrpcSlServer(ip string, port int) {
	conf := settlement.GrpcConfig{
		Host: ip,
		Port: port,
	}

	//kv := store.NewDefaultKVStore(".", "db", "settlement")
	lis, err := net.Listen("tcp", conf.Host+":"+strconv.Itoa(conf.Port))
	if err != nil {
		log.Panic(err)
	}
	log.Println("SL Grpc Server Listening on:", lis.Addr())
	srv := mockserv.GetServer(conf)
	if err := srv.Serve(lis); err != nil {
		log.Println("error while serving:", err)
	}
}
