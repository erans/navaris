package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"

	"golang.org/x/sys/unix"

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

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		log.Fatalf("agent: create AF_VSOCK socket: %v", err)
	}

	sa := &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: uint32(port),
	}
	if err := unix.Bind(fd, sa); err != nil {
		unix.Close(fd)
		log.Fatalf("agent: bind vsock port %d: %v", port, err)
	}

	if err := unix.Listen(fd, 128); err != nil {
		unix.Close(fd)
		log.Fatalf("agent: listen vsock: %v", err)
	}

	// Wrap the raw fd into a net.Listener so the agent package can use it.
	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", port))
	ln, err := net.FileListener(f)
	f.Close() // FileListener dup's the fd; close our copy.
	if err != nil {
		log.Fatalf("agent: wrap fd as listener: %v", err)
	}

	log.Printf("agent: listening on vsock port %d", port)
	agent.NewServer(ln).Serve()
}
