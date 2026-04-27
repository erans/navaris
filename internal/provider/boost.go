package provider

import (
	"context"
	"net"
)

// BoostServer is the interface that boost-channel handlers must implement.
// It is accepted by SetBoostHandler on provider implementations that support
// the in-sandbox boost channel (Firecracker and Incus). Placing the interface
// here avoids import cycles: both provider sub-packages import this package,
// and cmd/navarisd references it when wiring the handler after service
// construction.
type BoostServer interface {
	Serve(ctx context.Context, conn net.Conn, sandboxID string)
}
