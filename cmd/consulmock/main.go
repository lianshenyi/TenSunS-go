// Command consulmock runs the in-memory Consul fixture for local smoke tests.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8500", "listen address")
	flag.Parse()
	srv := consulmock.New()
	if err := srv.SeedDemo(); err != nil {
		log.Fatal(err)
	}
	log.Printf("consul mock listening on http://%s/v1", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
