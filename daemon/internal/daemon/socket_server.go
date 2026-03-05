package daemon

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
)

type SocketServer struct {
	config         *types.DaemonConfig
	unixListener   net.Listener
	tcpListener    net.Listener
	commandHandler *CommandHandler
}

func NewSocketServer(config *types.DaemonConfig, commandHandler *CommandHandler) *SocketServer {
	return &SocketServer{
		config:         config,
		commandHandler: commandHandler,
	}
}

func (ss *SocketServer) Start() error {
	if err := ss.startUnixListener(); err != nil {
		return err
	}
	return ss.startTCPListener()
}

func (ss *SocketServer) startUnixListener() error {
	if ss.config.SocketPath == "" {
		return nil
	}
	ss.cleanupSocket()
	ul, err := net.Listen("unix", ss.config.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on unix socket: %w", err)
	}
	ss.unixListener = ul
	if err := ss.setSocketPermissions(); err != nil {
		return err
	}
	log.Printf("[socket] Listening on unix:%s", ss.config.SocketPath)
	return nil
}

func (ss *SocketServer) startTCPListener() error {
	if ss.config.TCPListenAddr == "" {
		return nil
	}

	var tl net.Listener
	var err error

	if ss.config.TLSCertFile != "" && ss.config.TLSKeyFile != "" {
		tlsConfig, err := ss.loadTLSConfig()
		if err != nil {
			return fmt.Errorf("failed to load TLS config: %w", err)
		}
		tl, err = tls.Listen("tcp", ss.config.TCPListenAddr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to listen on tcp+tls: %w", err)
		}
		log.Printf("[socket] Listening on tcp+tls:%s (mTLS: %v)", ss.config.TCPListenAddr, ss.config.TLSCAFile != "")
	} else {
		tl, err = net.Listen("tcp", ss.config.TCPListenAddr)
		if err != nil {
			return fmt.Errorf("failed to listen on tcp: %w", err)
		}
		log.Printf("[socket] Listening on tcp:%s (INSECURE)", ss.config.TCPListenAddr)
	}
	ss.tcpListener = tl
	return nil
}

func (ss *SocketServer) loadTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(ss.config.TLSCertFile, ss.config.TLSKeyFile)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if ss.config.TLSCAFile != "" {
		caCert, err := ioutil.ReadFile(ss.config.TLSCAFile)
		if err != nil {
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.ClientCAs = caCertPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsConfig, nil
}

func (ss *SocketServer) handleConnection(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	clientIdentity := conn.RemoteAddr().String()
	if clientIdentity == "" || clientIdentity == "@" {
		clientIdentity = "local-unix-socket"
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
	response := ss.commandHandler.HandleCommand(cmd, clientIdentity)
	CommandsHandled.Add(2)
	_ = encoder.Encode(response)
}

func (ss *SocketServer) cleanupSocket() {
	if _, err := os.Stat(ss.config.SocketPath); err == nil {
		_ = os.Remove(ss.config.SocketPath)
	}
}

func (ss *SocketServer) setSocketPermissions() error {
	// #nosec G302
	if err := os.Chmod(ss.config.SocketPath, 0660); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}
	g, err := user.LookupGroup("nextdeploy")
	var gid int
	if err == nil {
		gid, _ = strconv.Atoi(g.Gid)
		if chownErr := os.Chown(ss.config.SocketPath, 0, gid); chownErr != nil {
			log.Printf("[socket] Warning: failed to chown socket to nextdeploy group: %v", chownErr)
		}
	} else {
		log.Printf("[socket] Warning: nextdeploy group not found, socket group ownership not set: %v", err)
	}

	socketDir := filepath.Dir(ss.config.SocketPath)
	if socketDir != "/var/run" && socketDir != "/run" {
		// #nosec G302
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
	if ss.unixListener != nil {
		go ss.acceptOnListener(ss.unixListener)
	}
	if ss.tcpListener != nil {
		go ss.acceptOnListener(ss.tcpListener)
	}
}

func (ss *SocketServer) acceptOnListener(l net.Listener) {
	for {
		conn, err := l.Accept()
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
	var errs []error
	if ss.unixListener != nil {
		if err := ss.unixListener.Close(); err != nil {
			errs = append(errs, err)
		}
		_ = os.Remove(ss.config.SocketPath)
	}
	if ss.tcpListener != nil {
		if err := ss.tcpListener.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing listeners: %v", errs)
	}
	return nil
}
