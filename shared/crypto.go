//go:build !windows
// +build !windows

package shared

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	KeyIDLength       = 32
	NonceSize         = 12
	SignatureSize     = ed25519.SignatureSize // 64 bytes for Ed25519 signature
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

func SignMessage(msg AgentMessage, privateKey *ecdsa.PrivateKey) (AgentMessage, error) {
	msgToSign := msg
	msgToSign.Signature = ""

	jsonData, err := json.Marshal(msgToSign)
	if err != nil {
		return AgentMessage{}, err
	}

	hash := sha256.Sum256(jsonData)
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, hash[:])
	if err != nil {
		return AgentMessage{}, err
	}

	signature, err := asn1.Marshal(ECCSignature{R: r, S: s})
	if err != nil {
		return AgentMessage{}, err
	}

	msg.Signature = string(signature)
	return msg, nil
}

func VerifyMessageSignature(msg AgentMessage) bool {
	if msg.Signature == "" {
		return false
	}

	publicKey := getAgentPublicKey(msg.AgentID)
	if publicKey == nil {
		return false
	}

	msgToVerify := msg
	msgToVerify.Signature = ""

	jsonData, err := json.Marshal(msgToVerify)
	if err != nil {
		return false
	}

	hash := sha256.Sum256(jsonData)

	var sig ECCSignature
	if _, err := asn1.Unmarshal([]byte(msg.Signature), &sig); err != nil {
		return false
	}

	return ecdsa.Verify(publicKey, hash[:], sig.R, sig.S)
}

func SecureKeyMemory(key []byte) {
	if len(key) == 0 {
		return
	}

	if _, _, err := syscall.Syscall(syscall.SYS_MLOCK, uintptr(unsafe.Pointer(&key[0])), uintptr(len(key)), 0); err != 0 {
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

	if _, _, err := syscall.Syscall(syscall.SYS_MUNLOCK, uintptr(unsafe.Pointer(&key[0])), uintptr(len(key)), 0); err != 0 {
		log.Printf("Warning: failed to unlock memory: %v", err)
	}
}

func GenerateKeyPair() (*KeyPair, error) {
	curve := ecdh.X25519()
	ecdhPrivate, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		SharedLogger.Error("Failed to generate ECDH key pair: %v", err)
		return nil, fmt.Errorf("failed to generate ECDH key pair: %w", err)
	}
	ecdhPublic := ecdhPrivate.PublicKey()
	if ecdhPublic == nil {
		SharedLogger.Error("Failed to get ECDH public key:%v", err)
		return nil, errors.New("failed to get ECDH public key")
	}
	KeyID := make([]byte, KeyIDLength)
	if _, err := io.ReadFull(rand.Reader, KeyID); err != nil {
		SharedLogger.Error("Failed to generate random KeyID: %v", err)
		return nil, fmt.Errorf("failed to generate random KeyID: %w", err)
	}
	signPublic, signPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		SharedLogger.Error("Failed to generate Ed25519 key pair: %v", err)
		return nil, fmt.Errorf("failed to generate Ed25519 keyManager pair: %w", err)
	}

	return &KeyPair{
		ECDHPrivate: ecdhPrivate,
		ECDHPublic:  ecdhPublic,
		SignPrivate: signPrivate,
		SignPublic:  signPublic,
		KeyID:       hex.EncodeToString(KeyID),
	}, nil

}
func RunCryptoHealthChecks() error {
	// TODO:Test ECDH key exchange
	return nil
}

func getAgentPublicKey(agentID string) *ecdsa.PublicKey {
	SharedLogger.Warn("getAgentPublicKey is not implemented, returning nil")
	return nil
}

func DeriveSharedKey(privateKey *ecdh.PrivateKey, publicKey *ecdh.PublicKey) ([]byte, error) {
	if privateKey == nil || publicKey == nil {
		SharedLogger.Error("Private key or public key is nil")
		return nil, errors.New("private key or public key is nil")
	}
	sharedKey, err := privateKey.ECDH(publicKey)
	if err != nil {
		SharedLogger.Error("Failed to derive shared key: %v", err)
		return nil, fmt.Errorf("failed to derive shared key: %w", err)
	}
	hashedKey := sha256.Sum256(sharedKey)
	return hashedKey[:SharedKeySize], nil
}

func Encrypt(data []byte, key []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		SharedLogger.Error("Failed to create AES cipher: %v", err)
		return nil, nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		SharedLogger.Error("Failed to create GCM: %v", err)
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		SharedLogger.Error("Failed to generate nonce: %v", err)
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, data, nil)
	return ciphertext, nonce, nil
}

func Decrypt(cipherText []byte, key []byte, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		SharedLogger.Error("failed to create AES cipher: %v", err)
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		SharedLogger.Error("failed to create GCM: %v", err)
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil {
		SharedLogger.Error("failed to decrypt data: %v", err)
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}
	return plaintext, nil
}

func Sign(data []byte, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != PrivateKeySize {
		SharedLogger.Error("Invalid private key size: expected %d, got %d", PrivateKeySize, len(privateKey))
		return nil, fmt.Errorf("invalid private key size: expected %d, got %d", PrivateKeySize, len(privateKey))
	}
	signature := ed25519.Sign(privateKey, data)
	if len(signature) != SignatureSize {
		SharedLogger.Error("Invalid signature size: expected %d, got %d", SignatureSize, len(signature))
		return nil, fmt.Errorf("invalid signature size: expected %d, got %d", SignatureSize, len(signature))
	}
	SharedLogger.Info("Data signed successfully")
	return signature, nil
}

func Verify(data []byte, signature []byte, publicKey ed25519.PublicKey) (bool, error) {

	if len(publicKey) != ed25519.PrivateKeySize {
		SharedLogger.Error("Invalid public key size: expected %d, got %d", ed25519.PrivateKeySize, len(publicKey))
		return false, fmt.Errorf("invalid public key size: expected %d, got %d", ed25519.PrivateKeySize, len(publicKey))
	}
	if len(signature) != SignatureSize {
		SharedLogger.Error("Invalid signature size: expected %d, got %d", SignatureSize, len(signature))
		return false, fmt.Errorf("invalid signature size: expected %d, got %d", SignatureSize, len(signature))
	}
	valid := ed25519.Verify(publicKey, data, signature)
	if !valid {
		SharedLogger.Error("Signature verification failed")
		return false, errors.New("signature verification failed")
	}
	SharedLogger.Info("Signature verified successfully")
	return true, nil
}

func GenerateFingerprint(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != PublicKeySize {
		SharedLogger.Error("Invalid public key size: expected %d, got %d", PublicKeySize, len(publicKey))
		return "", fmt.Errorf("invalid public key size: expected %d, got %d", PublicKeySize, len(publicKey))
	}
	hash := sha256.Sum256(publicKey)
	fingerprint := hex.EncodeToString(hash[:FingerprintLength])
	return fingerprint, nil
}

func LoadKeyFromFile(filename string) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		SharedLogger.Error("Failed to open key file: %v", err)
		return nil, fmt.Errorf("failed to open key file: %w", err)
	}
	defer file.Close()
	key := make([]byte, PrivateKeySize)
	_, err = file.Read(key)
	if err != nil {
		SharedLogger.Error("Failed to read key from file: %v", err)
		return nil, fmt.Errorf("failed to read key from file: %w", err)
	}
	if len(key) != PrivateKeySize {
		SharedLogger.Error("Invalid key size: expected %d, got %d", PrivateKeySize, len(key))
		return nil, fmt.Errorf("invalid key size: expected %d, got %d", PrivateKeySize, len(key))
	}
	return key, nil
}
