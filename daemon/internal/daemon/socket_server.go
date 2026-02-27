package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"

	"golang.org/x/time/rate"
)

type SocketServer struct {
	socketPath     string
	listener       net.Listener
	commandHandler *CommandHandler
	limiter        *rate.Limiter
}

func NewSocketServer(socketPath string, commandHandler *CommandHandler) *SocketServer {
	return &SocketServer{
		socketPath:     socketPath,
		commandHandler: commandHandler,
		// Allow 10 requests per second, with a burst of 20
		limiter: rate.NewLimiter(rate.Limit(10), 20),
	}
}

func (ss *SocketServer) Start() error {
	// remove existing scoket file if it exists
	ss.cleanupSocket()

	listener, err := net.Listen("unix", ss.socketPath)
	if err != nil {
		return err
	}
	ss.listener = listener

	return ss.setSocketPermissions()
}

func (ss *SocketServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	if !ss.limiter.Allow() {
		resp := types.Response{
			Success: false,
			Message: "rate limit exceeded",
		}
		_ = encoder.Encode(resp)
		return
	}

	var cmd types.Command

	if err := decoder.Decode(&cmd); err != nil {
		return
	}
	if err := ss.commandHandler.ValidateCommand(cmd); err != nil {
		resp := types.Response{
			Success: false,
			Message: fmt.Sprintf("invalid command: %v", err),
		}
		_ = encoder.Encode(resp)
		return
	}
	response := ss.commandHandler.HandleCommand(cmd)
	_ = encoder.Encode(response)
}

func (ss *SocketServer) cleanupSocket() {
	if _, err := os.Stat(ss.socketPath); err == nil {
		_ = os.Remove(ss.socketPath)
	}
}

func (ss *SocketServer) setSocketPermissions() error {
	// #nosec G302
	if err := os.Chmod(ss.socketPath, 0660); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}
	// get the socket directory and ensure its secure
	socketDir := filepath.Dir(ss.socketPath)
	// #nosec G302
	if err := os.Chmod(socketDir, 0700); err != nil {
		return fmt.Errorf("failed to set socket directory permissions: %w", err)
	}
	return nil
}

func (ss *SocketServer) AcceptConnections() {
	for {
		conn, err := ss.listener.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return
		}
		go ss.handleConnection(conn)
	}
}
func (ss *SocketServer) Close() error {
	if ss.listener != nil {
		return ss.listener.Close()
	}
	// clean up socket file
	_ = os.Remove(ss.socketPath)
	return nil
}
