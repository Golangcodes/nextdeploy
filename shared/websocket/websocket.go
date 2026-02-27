package websocket

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"net/http"
	"nextdeploy/shared"
	"time"

	"github.com/gorilla/websocket"
)

var (
	websockerlogger = shared.PackageLogger("api", "🌐 API")
)

func NewWSClient(agentID string, privateKey *ecdsa.PrivateKey) *WSClient {
	return &WSClient{
		agentID:    agentID,
		privateKey: privateKey,
		pingPeriod: 15 * time.Second,
		writeWait:  10 * time.Second,
		pongWait:   20 * time.Second,
	}
}

func (c *WSClient) ReceiveMessage() (shared.AgentMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		websockerlogger.Error("WebSocket client is not connected")
		return shared.AgentMessage{}, errors.New("websocket client is not connected")
	}

	messsageType, message, err := c.conn.ReadMessage()
	fmt.Printf("Received message type: %v", message)
	fmt.Printf("Message type: %d\n", messsageType)
	if err != nil {
		websockerlogger.Error("Failed to read message from WebSocket error:%s", err)
		return shared.AgentMessage{}, err
	}
	// FIX: Deserialize the message into AgentMessage and use returned message from ReadMessage
	var agentMessage shared.AgentMessage
	return agentMessage, nil
}
func (c *WSClient) Connect(url string, headers http.Header) error {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(url, headers)
	if err != nil {
		websockerlogger.Error("Failed to connect to WebSocket server:%s", err)
		return err
	}

	c.conn = conn
	c.connected = true

	// Start reader/writer goroutines
	go c.readPump()
	go c.writePump()

	return nil

}
func (c *WSClient) SendMessage(msg interface{}) error {

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		websockerlogger.Error("WebSocket client is not connected")
		return errors.New("websocket client is not connected")
	}

	return c.conn.WriteJSON(msg)

}

func (c *WSClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		websockerlogger.Error("WebSocket client is not connected")
		return errors.New("websocket client is not connected")
	}

	err := c.conn.Close()
	if err != nil {
		websockerlogger.Error("Failed to close WebSocket connection error:%s", err)
		return err
	}

	c.connected = false
	return nil
}

func (c *WSClient) readPump() {
	defer c.Close()

	_ = c.conn.SetReadDeadline(time.Now().Add(c.pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(c.pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				websockerlogger.Error("WebSocket read error:%s", err)
			}
			return
		}
		websockerlogger.Info("Received message from WebSocket server message:%s", string(message))
	}

}

func (c *WSClient) writePump() {
	ticker := time.NewTicker(c.pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.Close()
	}()

	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				websockerlogger.Error("Failed to send ping error:%s", err)
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()

		default:
			time.Sleep(1 * time.Second) // Avoid busy waiting
		}
	}
}
