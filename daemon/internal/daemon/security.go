package daemon

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"sync"
	"time"
)

type RateLimiter struct {
	tokens map[string]float64
	last   map[string]time.Time
	mu     sync.Mutex
	rate   float64
	burst  float64
}

func NewRateLimiter(rate, burst float64) *RateLimiter {
	return &RateLimiter{
		tokens: make(map[string]float64),
		last:   make(map[string]time.Time),
		rate:   rate,
		burst:  burst,
	}
}

func (rl *RateLimiter) Allow(id string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	// Initialize if not present
	if _, ok := rl.tokens[id]; !ok {
		rl.tokens[id] = rl.burst
		rl.last[id] = now
	}

	// Refill tokens
	elapsed := now.Sub(rl.last[id]).Seconds()
	rl.tokens[id] += elapsed * rl.rate
	if rl.tokens[id] > rl.burst {
		rl.tokens[id] = rl.burst
	}
	rl.last[id] = now

	if rl.tokens[id] >= 1.0 {
		rl.tokens[id] -= 1.0
		return true
	}
	return false
}

func VerifySignature(payload []byte, signature, secret string) bool {
	if secret == "" {
		return true // Security disabled if no secret is configured
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	expected := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

func IsIPAllowed(ipStr string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true // No whitelist means all are allowed (standard behavior for local deployments)
	}

	// Remove port if present
	host, _, err := net.SplitHostPort(ipStr)
	if err == nil {
		ipStr = host
	}

	clientIP := net.ParseIP(ipStr)
	if clientIP == nil {
		return false
	}

	for _, cidr := range whitelist {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			if ipNet.Contains(clientIP) {
				return true
			}
		} else {
			// Try exact IP match if not a CIDR
			if cidr == ipStr {
				return true
			}
		}
	}
	return false
}
