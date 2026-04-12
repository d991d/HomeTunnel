// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// Package encryption provides all cryptographic primitives used by HomeTunnel:
//   - X25519 ephemeral key generation and Diffie-Hellman
//   - HKDF-SHA256 session-key derivation
//   - ChaCha20-Poly1305 AEAD encryption / decryption
//   - AES-256-GCM AEAD (fallback)
//   - Nonce derivation from (sessionID, seqNum)
//   - HMAC-SHA256 token signing / verification
//   - Sliding-window replay protection
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// ─── Constants ────────────────────────────────────────────────────────────────

// HKDFInfo is the application-specific context string used in HKDF.
const HKDFInfo = "hometunnel-v1-session-key"

// NonceSize is 12 bytes (96-bit) for both ChaCha20-Poly1305 and AES-256-GCM.
const NonceSize = 12

// KeySize is 32 bytes for ChaCha20-Poly1305 and AES-256-GCM.
const KeySize = 32

// TagSize is the 16-byte AEAD authentication tag.
const TagSize = 16

// Cipher identifiers negotiated during handshake.
const (
	CipherChaCha20Poly1305 uint8 = 0x01
	CipherAES256GCM        uint8 = 0x02
)

// Errors.
var (
	ErrDecryptFailed   = errors.New("encryption: AEAD decryption failed")
	ErrInvalidKeySize  = errors.New("encryption: invalid key size")
	ErrReplayDetected  = errors.New("encryption: replay detected")
	ErrWindowExceeded  = errors.New("encryption: sequence number below window")
)

// ─── X25519 Key Pair ──────────────────────────────────────────────────────────

// KeyPair holds an X25519 ephemeral key pair.
type KeyPair struct {
	Private [32]byte
	Public  [32]byte
}

// GenerateKeyPair creates a fresh ephemeral X25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	kp := &KeyPair{}
	if _, err := io.ReadFull(rand.Reader, kp.Private[:]); err != nil {
		return nil, err
	}
	// Clamp private key per RFC 7748
	kp.Private[0] &= 248
	kp.Private[31] &= 127
	kp.Private[31] |= 64

	pub, err := curve25519.X25519(kp.Private[:], curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	copy(kp.Public[:], pub)
	return kp, nil
}

// SharedSecret computes the X25519 shared secret: scalar × peerPublic.
func SharedSecret(myPrivate, peerPublic [32]byte) ([32]byte, error) {
	out, err := curve25519.X25519(myPrivate[:], peerPublic[:])
	if err != nil {
		return [32]byte{}, err
	}
	var secret [32]byte
	copy(secret[:], out)
	return secret, nil
}

// ─── HKDF Key Derivation ─────────────────────────────────────────────────────

// DeriveSessionKey derives a 32-byte session key from a shared DH secret using
// HKDF-SHA256. salt should be a randomly generated value (e.g. the challenge).
func DeriveSessionKey(sharedSecret [32]byte, salt []byte) ([KeySize]byte, error) {
	r := hkdf.New(sha256.New, sharedSecret[:], salt, []byte(HKDFInfo))
	var key [KeySize]byte
	if _, err := io.ReadFull(r, key[:]); err != nil {
		return [KeySize]byte{}, err
	}
	return key, nil
}

// ─── Nonce Derivation ─────────────────────────────────────────────────────────

// DeriveNonce builds the 12-byte AEAD nonce from sessionID and seqNum.
//
//	nonce[0:4]  = sessionID  (big-endian uint32)
//	nonce[4:12] = seqNum     (big-endian uint64)
func DeriveNonce(sessionID uint32, seqNum uint64) [NonceSize]byte {
	var n [NonceSize]byte
	binary.BigEndian.PutUint32(n[0:4], sessionID)
	binary.BigEndian.PutUint64(n[4:12], seqNum)
	return n
}

// ─── AEAD Session ─────────────────────────────────────────────────────────────

// Session wraps an AEAD cipher and provides Seal/Open with automatic nonce
// derivation and sequence-number management.
type Session struct {
	aead      cipher.AEAD
	sessionID uint32
	cipherID  uint8

	// Outbound sequence counter (incremented on each Seal call)
	sendSeq uint64
	sendMu  sync.Mutex

	// Replay window for inbound packets
	replayWindow *ReplayWindow
}

// NewSession creates a new encryption Session from a 32-byte key.
// cipherID must be CipherChaCha20Poly1305 or CipherAES256GCM.
func NewSession(key [KeySize]byte, sessionID uint32, cipherID uint8) (*Session, error) {
	var aead cipher.AEAD
	var err error

	switch cipherID {
	case CipherChaCha20Poly1305:
		aead, err = chacha20poly1305.New(key[:])
	case CipherAES256GCM:
		block, e := aes.NewCipher(key[:])
		if e != nil {
			return nil, e
		}
		aead, err = cipher.NewGCM(block)
	default:
		// Default to ChaCha20-Poly1305
		aead, err = chacha20poly1305.New(key[:])
		cipherID = CipherChaCha20Poly1305
	}
	if err != nil {
		return nil, err
	}

	return &Session{
		aead:         aead,
		sessionID:    sessionID,
		cipherID:     cipherID,
		replayWindow: NewReplayWindow(256),
	}, nil
}

// Seal encrypts and authenticates plaintext.
// Returns (ciphertext, seqNum used) — the caller embeds seqNum in the packet header.
func (s *Session) Seal(plaintext, additionalData []byte) ([]byte, uint64) {
	s.sendMu.Lock()
	seq := s.sendSeq
	s.sendSeq++
	s.sendMu.Unlock()

	nonce := DeriveNonce(s.sessionID, seq)
	ct := s.aead.Seal(nil, nonce[:], plaintext, additionalData)
	return ct, seq
}

// Open decrypts and authenticates ciphertext received with the given seqNum.
// It also enforces replay protection.
func (s *Session) Open(ciphertext []byte, seqNum uint64, additionalData []byte) ([]byte, error) {
	// Replay check BEFORE decryption (cheap)
	if err := s.replayWindow.Check(seqNum); err != nil {
		return nil, err
	}
	nonce := DeriveNonce(s.sessionID, seqNum)
	pt, err := s.aead.Open(nil, nonce[:], ciphertext, additionalData)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	// Mark as seen only after successful decryption
	s.replayWindow.Mark(seqNum)
	return pt, nil
}

// CipherID returns the negotiated cipher identifier.
func (s *Session) CipherID() uint8 { return s.cipherID }

// ─── Replay Window ────────────────────────────────────────────────────────────

// ReplayWindow is a concurrent-safe sliding window for sequence-number replay detection.
type ReplayWindow struct {
	mu      sync.Mutex
	size    uint64
	maxSeen uint64
	bitmap  []uint64 // 1 bit per slot; each uint64 covers 64 slots
}

// NewReplayWindow creates a window of windowSize slots (should be a multiple of 64).
func NewReplayWindow(windowSize uint) *ReplayWindow {
	words := (windowSize + 63) / 64
	return &ReplayWindow{
		size:   uint64(windowSize),
		bitmap: make([]uint64, words),
	}
}

// Check returns nil if seqNum is acceptable (not yet seen and within window).
func (w *ReplayWindow) Check(seqNum uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if seqNum+w.size <= w.maxSeen {
		return ErrWindowExceeded
	}
	if seqNum <= w.maxSeen {
		idx := w.maxSeen - seqNum
		word := idx / 64
		bit := idx % 64
		if int(word) < len(w.bitmap) && (w.bitmap[word]>>bit)&1 == 1 {
			return ErrReplayDetected
		}
	}
	return nil
}

// Mark records seqNum as seen and advances the window if needed.
func (w *ReplayWindow) Mark(seqNum uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if seqNum > w.maxSeen {
		advance := seqNum - w.maxSeen
		w.shiftWindow(advance)
		w.maxSeen = seqNum
	}
	idx := w.maxSeen - seqNum
	word := idx / 64
	bit := idx % 64
	if int(word) < len(w.bitmap) {
		w.bitmap[word] |= 1 << bit
	}
}

// shiftWindow advances the bitmap by n positions.
func (w *ReplayWindow) shiftWindow(n uint64) {
	if n >= w.size {
		// Clear entire window
		for i := range w.bitmap {
			w.bitmap[i] = 0
		}
		return
	}
	words := n / 64
	bits := n % 64
	l := len(w.bitmap)
	// Left-shift the big-endian bitmap so that older sequence numbers move to
	// higher bit positions (idx = maxSeen - seqNum grows as maxSeen advances).
	for i := l - 1; i >= 0; i-- {
		src := i - int(words)
		if src < 0 {
			w.bitmap[i] = 0
			continue
		}
		w.bitmap[i] = w.bitmap[src] << bits
		if bits > 0 && src-1 >= 0 {
			w.bitmap[i] |= w.bitmap[src-1] >> (64 - bits)
		}
	}
}

// ─── HMAC helpers ─────────────────────────────────────────────────────────────

// HMACSign computes HMAC-SHA256(key, message).
func HMACSign(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

// HMACVerify returns true if HMAC-SHA256(key, message) == sig (constant time).
func HMACVerify(key, message, sig []byte) bool {
	expected := HMACSign(key, message)
	return hmac.Equal(expected, sig)
}

// ─── Random helpers ───────────────────────────────────────────────────────────

// RandomBytes returns n cryptographically random bytes.
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}

// Random16 returns a 16-byte random value.
func Random16() ([16]byte, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return [16]byte{}, err
	}
	return b, nil
}

// ─── Header obfuscation ───────────────────────────────────────────────────────

// ObfuscateHeader XOR-masks the first 2 bytes of buf using a key derived from
// sessionKey. It is its own inverse (applying twice restores the original).
func ObfuscateHeader(buf []byte, sessionKey [KeySize]byte) {
	if len(buf) < 2 {
		return
	}
	h := sha256.Sum256(append(sessionKey[:], []byte("header-xor")...))
	buf[0] ^= h[0]
	buf[1] ^= h[1]
}
