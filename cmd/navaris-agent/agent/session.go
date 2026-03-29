package agent

import (
	"encoding/json"
	"net"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

func HandleSession(req *vsock.Message, send SendFunc, conn net.Conn) {
	var payload vsock.SessionPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		sendExit(send, req.ID, -1)
		return
	}
	sendExit(send, req.ID, -1)
}
