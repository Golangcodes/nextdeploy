package client

import (
	"encoding/json"
	"fmt"
	"net"
	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
	"time"
)

func SendCommand(socketPath string, cmd types.Command) (*types.Response, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to socket: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if err := encoder.Encode(cmd); err != nil {
		return nil, fmt.Errorf("failed to send command: %w", err)
	}

	var resp types.Response
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	return &resp, nil
}
