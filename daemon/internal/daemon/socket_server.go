package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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
		limiter:        rate.NewLimiter(rate.Limit(10), 20),
	}
}

func (ss *SocketServer) Start() error {
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
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
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
	CommandsHandled.Add(1)
	_ = encoder.Encode(response)
}

func (ss *SocketServer) cleanupSocket() {
	if _, err := os.Stat(ss.socketPath); err == nil {
		_ = os.Remove(ss.socketPath)
	}
}

func (ss *SocketServer) setSocketPermissions() error {
	if err := os.Chmod(ss.socketPath, 0660); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}
	g, err := user.LookupGroup("nextdeploy")
	var gid int
	if err == nil {
		gid, _ = strconv.Atoi(g.Gid)
		if chownErr := os.Chown(ss.socketPath, 0, gid); chownErr != nil {
			log.Printf("[socket] Warning: failed to chown socket to nextdeploy group: %v", chownErr)
		}
	} else {
		log.Printf("[socket] Warning: nextdeploy group not found, socket group ownership not set: %v", err)
	}

	socketDir := filepath.Dir(ss.socketPath)
	if socketDir != "/var/run" && socketDir != "/run" {
		if err := os.Chmod(socketDir, 0770); err != nil {
			return fmt.Errorf("failed to set socket directory permissions: %w", err)
		}
		if g != nil {
			_ = os.Chown(socketDir, 0, gid)
		}
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
