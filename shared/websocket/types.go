package websocket

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type WSClient struct {
	conn        *websocket.Conn
	mu          sync.Mutex
	agentID     string
	privateKey  *ecdsa.PrivateKey
	connected   bool
	pingPeriod  time.Duration
	writeWait   time.Duration
	pongWait    time.Duration
	authKey     *ecdsa.PrivateKey
	sessionKeys map[string]*ecdh.PrivateKey
	upgrader    websocket.Upgrader
}

type SecureMessage struct {
	IV         []byte `json:"iv"`
	Ciphertext []byte `json:"ciphertext"`
	Tag        []byte `json:"tag"`
	Sequence   uint64 `json:"sequence"`
}
