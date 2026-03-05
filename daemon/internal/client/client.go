package client

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/daemon/internal/types"
)

type ClientConfig struct {
	Address    string
	Secret     string
	CertFile   string
	KeyFile    string
	CAFile     string
	SkipVerify bool
}

func SendCommand(cfg ClientConfig, cmd types.Command) (*types.Response, error) {
	var conn net.Conn
	var err error

	if strings.HasPrefix(cfg.Address, "/") || (!strings.Contains(cfg.Address, ":") && !strings.Contains(cfg.Address, "tcp")) {
		// Unix socket
		conn, err = net.Dial("unix", cfg.Address)
	} else {
		// TCP
		if cfg.CertFile != "" && cfg.KeyFile != "" {
			tlsConfig, err := loadTLSConfig(cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to load TLS config: %w", err)
			}
			conn, err = tls.Dial("tcp", cfg.Address, tlsConfig)
		} else {
			conn, err = net.Dial("tcp", cfg.Address)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon at %s: %w", cfg.Address, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))

	// Sign the command
	if cfg.Secret != "" {
		payload, _ := json.Marshal(map[string]interface{}{
			"type": cmd.Type,
			"args": cmd.Args,
		})
		h := hmac.New(sha256.New, []byte(cfg.Secret))
		h.Write(payload)
		cmd.Signature = hex.EncodeToString(h.Sum(nil))
	}

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

func loadTLSConfig(cfg ClientConfig) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: cfg.SkipVerify,
		MinVersion:         tls.VersionTLS12,
	}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}
