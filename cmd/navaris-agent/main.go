package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"syscall"

	"github.com/mdlayher/vsock"
	"github.com/navaris/navaris/cmd/navaris-agent/agent"
)

const defaultPort = 1024

func main() {
	ensureDevpts()

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

	go func() {
		if err := agent.RunBoostProxy(context.Background(), "/var/run/navaris-guest.sock", 1025); err != nil {
			log.Printf("agent: boost proxy: %v", err)
		}
	}()

	log.Printf("agent: listening on vsock port %d", port)
	agent.NewServer(ln).Serve()
}

// ensureDevpts mounts the devpts filesystem at /dev/pts if it is not
// already mounted. PTY allocation requires this.
func ensureDevpts() {
	if _, err := os.Stat("/dev/pts/ptmx"); err == nil {
		return // already mounted
	}
	if err := os.MkdirAll("/dev/pts", 0755); err != nil {
		log.Printf("agent: mkdir /dev/pts: %v", err)
		return
	}
	if err := syscall.Mount("devpts", "/dev/pts", "devpts", 0, ""); err != nil {
		log.Printf("agent: mount devpts: %v", err)
	}
}
