// Command mcp-oauth-mock runs a disposable local OAuth/MCP recipe server.
package main

import (
	"flag"
	"github.com/rclsilver/threavia/internal/dev/mcpmock"
	"log"
	"net"
	"net/http"
	"time"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:18084", "Loopback listen address")
	base := flag.String("base-url", "http://127.0.0.1:18084", "Public loopback origin, matching listen")
	flag.Parse()
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("mock must listen on a loopback IP")
	}
	handler, err := mcpmock.New(*base)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("OAuth MCP mock listening on %s (fictional data only)", *addr)
	log.Fatal((&http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}).ListenAndServe())
}
