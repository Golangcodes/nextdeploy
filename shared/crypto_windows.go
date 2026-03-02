//go:build windows
// +build windows

package shared

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"log"
	"math/big"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func SecureKeyMemory(key []byte) {
	if len(key) == 0 {
		return
	}

	addr := uintptr(unsafe.Pointer(&key[0]))
	length := uintptr(len(key))

	err := windows.VirtualLock(addr, length)
	if err != nil {
		log.Printf("Warning: failed to lock memory: %v", err)
	}
}

func ZeroKey(key []byte) {
	if len(key) == 0 {
		return
	}

	for i := range key {
		key[i] = 0
	}

	runtime.KeepAlive(key)
	addr := uintptr(unsafe.Pointer(&key[0]))
	length := uintptr(len(key))

	err := windows.VirtualUnlock(addr, length)
	if err != nil {
		log.Printf("Warning: failed to unlock memory: %v", err)
	}
}

const (
	KeyIDLength       = 32
	NonceSize         = 12
	SignatureSize     = ed25519.SignatureSize
	PublicKeySize     = 32
	PrivateKeySize    = 32
	SharedKeySize     = 32
	FingerprintLength = 16
)

type ECCSignature struct {
	R *big.Int
	S *big.Int
}

var (
	SharedLogger = PackageLogger("shared", "🔗 SHARED")
)

type KeyPair struct {
	ECDHPrivate *ecdh.PrivateKey
	ECDHPublic  *ecdh.PublicKey
	SignPrivate ed25519.PrivateKey
	SignPublic  ed25519.PublicKey
	ECDSAKey    *ecdsa.PrivateKey
	KeyID       string
}
