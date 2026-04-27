package agent

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/mdlayher/vsock"
)

// RunBoostProxy serves HTTP-over-Unix-socket at listenPath; each inbound
// connection is piped to a fresh AF_VSOCK conn to (CID=2, port=vsockPort).
// The proxy is a dumb byte-pipe — both sides of the proxy speak HTTP.
//
// Caller is responsible for unlinking listenPath if it already exists.
func RunBoostProxy(ctx context.Context, listenPath string, vsockPort uint32) error {
	_ = os.Remove(listenPath)
	listener, err := net.Listen("unix", listenPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(listenPath)

	// Mode 0666 so any process inside the sandbox can connect.
	_ = os.Chmod(listenPath, 0o666)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("agent: boost proxy accept: %v", err)
			continue
		}
		go pipeToVsock(ctx, conn, vsockPort)
	}
}

func pipeToVsock(ctx context.Context, in net.Conn, vsockPort uint32) {
	defer in.Close()

	out, err := vsock.Dial(2, vsockPort, nil)
	if err != nil {
		io.WriteString(in, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	defer out.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(out, in); out.Close() }()
	go func() { defer wg.Done(); io.Copy(in, out); in.Close() }()
	wg.Wait()
}
