package main

import (
	"log"
	"net"
	"os"
	"strconv"
	"time"

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

	log.Printf("agent: listening on vsock port %d", port)
	agent.NewServer(&vsockListener{fd: fd}).Serve()
}

// vsockListener implements net.Listener for AF_VSOCK sockets.
// Go's net.FileListener does not support AF_VSOCK, so we accept directly.
type vsockListener struct {
	fd int
}

func (l *vsockListener) Accept() (net.Conn, error) {
	nfd, _, err := unix.Accept(l.fd)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(nfd), "vsock-conn")
	return &vsockConn{f: f}, nil
}

func (l *vsockListener) Close() error {
	return unix.Close(l.fd)
}

func (l *vsockListener) Addr() net.Addr {
	return vsockAddr{}
}

// vsockConn wraps an os.File as a net.Conn for AF_VSOCK connections.
type vsockConn struct {
	f *os.File
}

func (c *vsockConn) Read(b []byte) (int, error)  { return c.f.Read(b) }
func (c *vsockConn) Write(b []byte) (int, error) { return c.f.Write(b) }
func (c *vsockConn) Close() error                { return c.f.Close() }
func (c *vsockConn) LocalAddr() net.Addr          { return vsockAddr{} }
func (c *vsockConn) RemoteAddr() net.Addr         { return vsockAddr{} }

func (c *vsockConn) SetDeadline(t time.Time) error      { return c.f.SetDeadline(t) }
func (c *vsockConn) SetReadDeadline(t time.Time) error   { return c.f.SetReadDeadline(t) }
func (c *vsockConn) SetWriteDeadline(t time.Time) error  { return c.f.SetWriteDeadline(t) }

type vsockAddr struct{}

func (vsockAddr) Network() string { return "vsock" }
func (vsockAddr) String() string  { return "vsock" }
