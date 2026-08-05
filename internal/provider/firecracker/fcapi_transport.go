//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/firecracker-microvm/firecracker-go-sdk/client"
	"github.com/go-openapi/runtime"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
)

// transientFirecrackerClient builds a low-level *client.Firecracker bound to
// a running VM's API socket with an idle-reaping transport. The caller
// issues one or two operations then lets it fall out of scope; the idle
// unix connection auto-reaps after idleTimeout, preventing FD retention
// between GC cycles. Use for one-shot API-socket calls; do NOT use for the
// long-lived Machine that manages a VMM lifecycle.
//
// F12: replaces fcsdk.NewMachine + thin SDK wrappers (UpdateBalloon,
// CreateSnapshot, Shutdown, PauseVM, ResumeVM) at the 3 transient call
// sites. The long-lived launch Machine is unchanged.
func transientFirecrackerClient(sockPath string, idleTimeout time.Duration) (*client.Firecracker, error) {
	if err := validateSockPath(sockPath); err != nil {
		return nil, err
	}
	fc := client.NewHTTPClient(strfmt.NewFormats())
	fc.SetTransport(buildIdleReapingTransport(sockPath, idleTimeout))
	return fc, nil
}

func buildIdleReapingTransport(sockPath string, idleTimeout time.Duration) runtime.ClientTransport {
	socketTransport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
		},
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     idleTimeout,
	}
	transport := httptransport.New(client.DefaultHost, client.DefaultBasePath, client.DefaultSchemes)
	transport.Transport = socketTransport
	return transport
}

func validateSockPath(sockPath string) error {
	if sockPath == "" {
		return fmt.Errorf("transientFirecrackerClient: socket path is empty")
	}
	return nil
}
