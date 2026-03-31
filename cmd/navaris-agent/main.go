package main

import (
	"log"
	"os"
	"strconv"

	"github.com/mdlayher/vsock"
	"github.com/navaris/navaris/cmd/navaris-agent/agent"
)

const defaultPort = 1024

func main() {
	port := defaultPort
	if v := os.Getenv("VSOCK_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("agent: invalid VSOCK_PORT %q: %v", v, err)
		}
		port = p
	}

	ln, err := vsock.Listen(uint32(port), nil)
	if err != nil {
		log.Fatalf("agent: listen vsock port %d: %v", port, err)
	}

	log.Printf("agent: listening on vsock port %d", port)
	agent.NewServer(ln).Serve()
}
