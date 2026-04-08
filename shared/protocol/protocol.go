// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// Package protocol defines the HomeTunnel wire format.
//
// Packet layout (all fields big-endian):
//
//	┌──────┬──────┬──────────────┬────────────────────────────────┐
//	│ Ver  │ Type │  Session ID  │       Sequence Number          │
//	│  1B  │  1B  │     4B       │            8B                  │
//	├──────┴──────┴──────────────┴────────────────────────────────┤
//	│               Timestamp (Unix nanoseconds, 8B)              │
//	├─────────────────────────────────────────────────────────────┤
//	│               Flags / Reserved (8B)                         │
//	├─────────────────────────────────────────────────────────────┤
//	│         AEAD-encrypted payload (variable length)            │
//	└─────────────────────────────────────────────────────────────┘
//
// Total fixed header size: 30 bytes.
package protocol

import (
	"encoding/binary"
	"errors"
	"time"
)

// Protocol version.
const Version = 0x01

// Packet types.
const (
	TypeHello      uint8 = 0x01 // Client → Server: initiate handshake
	TypeChallenge  uint8 = 0x02 // Server → Client: ephemeral key + challenge
	TypeAuth       uint8 = 0x03 // Client → Server: HMAC proof + token
	TypeAccept     uint8 = 0x04 // Server → Client: session ID + virtual IP
	TypeData       uint8 = 0x05 // Bidirectional: encrypted tunnel payload
	TypeKeepalive  uint8 = 0x06 // Bidirectional: liveness probe
	TypeDisconnect uint8 = 0x07 // Bidirectional: graceful teardown
	TypeReject     uint8 = 0x08 // Server → Client: authentication failed
)

// HeaderSize is the fixed byte length of every packet header.
const HeaderSize = 30

// MaxPacketSize is the maximum UDP payload we will send (header + payload).
// Stays well under typical 1500-byte MTU minus IP+UDP headers.
const MaxPacketSize = 1400

// MaxPaddingBytes is the maximum random padding added per packet (obfuscation).
const MaxPaddingBytes = 127

// ReplayWindowSize is the number of sequence slots in the sliding window.
const ReplayWindowSize = 256

// ReplayTimestampTolerance is the maximum allowed clock skew for replay detection.
const ReplayTimestampTolerance = 30 * time.Second

// Header is the decoded fixed-size packet header.
type Header struct {
	Version    uint8
	Type       uint8
	SessionID  uint32
	SeqNum     uint64
	Timestamp  int64  // Unix nanoseconds
	Flags      uint64 // reserved
}

// Packet is a fully decoded HomeTunnel packet.
type Packet struct {
	Header
	Payload []byte // plaintext after AEAD decryption (nil for handshake packets)
	Raw     []byte // original wire bytes (header + ciphertext)
}

// Errors returned by Encode/Decode.
var (
	ErrPacketTooShort  = errors.New("protocol: packet too short")
	ErrVersionMismatch = errors.New("protocol: unsupported protocol version")
)

// EncodeHeader serialises h into a 30-byte slice.
func EncodeHeader(h *Header) []byte {
	buf := make([]byte, HeaderSize)
	buf[0] = h.Version
	buf[1] = h.Type
	binary.BigEndian.PutUint32(buf[2:6], h.SessionID)
	binary.BigEndian.PutUint64(buf[6:14], h.SeqNum)
	binary.BigEndian.PutUint64(buf[14:22], uint64(h.Timestamp))
	binary.BigEndian.PutUint64(buf[22:30], h.Flags)
	return buf
}

// DecodeHeader parses the first HeaderSize bytes of b into a Header.
func DecodeHeader(b []byte) (*Header, error) {
	if len(b) < HeaderSize {
		return nil, ErrPacketTooShort
	}
	h := &Header{
		Version:   b[0],
		Type:      b[1],
		SessionID: binary.BigEndian.Uint32(b[2:6]),
		SeqNum:    binary.BigEndian.Uint64(b[6:14]),
		Timestamp: int64(binary.BigEndian.Uint64(b[14:22])),
		Flags:     binary.BigEndian.Uint64(b[22:30]),
	}
	if h.Version != Version {
		return nil, ErrVersionMismatch
	}
	return h, nil
}

// NewHeader returns a Header pre-filled with version and current timestamp.
func NewHeader(pktType uint8, sessionID uint32, seq uint64) *Header {
	return &Header{
		Version:   Version,
		Type:      pktType,
		SessionID: sessionID,
		SeqNum:    seq,
		Timestamp: time.Now().UnixNano(),
	}
}

// BuildPacket concatenates header bytes and ciphertext into a wire-ready slice.
func BuildPacket(h *Header, ciphertext []byte) []byte {
	hdr := EncodeHeader(h)
	pkt := make([]byte, len(hdr)+len(ciphertext))
	copy(pkt, hdr)
	copy(pkt[len(hdr):], ciphertext)
	return pkt
}

// ─── Handshake payload types ─────────────────────────────────────────────────

// HelloPayload is sent by the client to initiate the handshake.
type HelloPayload struct {
	ClientPubKey [32]byte // X25519 ephemeral public key
}

// EncodeHello serialises a HelloPayload.
func EncodeHello(p *HelloPayload) []byte {
	buf := make([]byte, 32)
	copy(buf, p.ClientPubKey[:])
	return buf
}

// DecodeHello parses a HelloPayload from b.
func DecodeHello(b []byte) (*HelloPayload, error) {
	if len(b) < 32 {
		return nil, ErrPacketTooShort
	}
	p := &HelloPayload{}
	copy(p.ClientPubKey[:], b[:32])
	return p, nil
}

// ChallengePayload is sent by the server in response to HELLO.
type ChallengePayload struct {
	ServerPubKey [32]byte // X25519 ephemeral public key
	Challenge    [16]byte // random 16-byte challenge nonce
}

// EncodeChallenge serialises a ChallengePayload.
func EncodeChallenge(p *ChallengePayload) []byte {
	buf := make([]byte, 48)
	copy(buf[:32], p.ServerPubKey[:])
	copy(buf[32:48], p.Challenge[:])
	return buf
}

// DecodeChallenge parses a ChallengePayload from b.
func DecodeChallenge(b []byte) (*ChallengePayload, error) {
	if len(b) < 48 {
		return nil, ErrPacketTooShort
	}
	p := &ChallengePayload{}
	copy(p.ServerPubKey[:], b[:32])
	copy(p.Challenge[:], b[32:48])
	return p, nil
}

// AuthPayload is sent by the client to authenticate.
// The token is AEAD-encrypted using the derived session key.
type AuthPayload struct {
	HMAC           [32]byte // HMAC-SHA256(sessionKey, challenge+token)
	EncryptedToken []byte   // AEAD-encrypted invite token
}

// EncodeAuth serialises an AuthPayload.
func EncodeAuth(p *AuthPayload) []byte {
	buf := make([]byte, 32+len(p.EncryptedToken))
	copy(buf[:32], p.HMAC[:])
	copy(buf[32:], p.EncryptedToken)
	return buf
}

// DecodeAuth parses an AuthPayload from b.
func DecodeAuth(b []byte) (*AuthPayload, error) {
	if len(b) < 32 {
		return nil, ErrPacketTooShort
	}
	p := &AuthPayload{
		EncryptedToken: make([]byte, len(b)-32),
	}
	copy(p.HMAC[:], b[:32])
	copy(p.EncryptedToken, b[32:])
	return p, nil
}

// AcceptPayload is sent by the server on successful authentication.
type AcceptPayload struct {
	SessionID uint32
	VirtualIP [4]byte // assigned IPv4 address in the VPN subnet
	MTU       uint16
}

// EncodeAccept serialises an AcceptPayload.
func EncodeAccept(p *AcceptPayload) []byte {
	buf := make([]byte, 10)
	binary.BigEndian.PutUint32(buf[0:4], p.SessionID)
	copy(buf[4:8], p.VirtualIP[:])
	binary.BigEndian.PutUint16(buf[8:10], p.MTU)
	return buf
}

// DecodeAccept parses an AcceptPayload from b.
func DecodeAccept(b []byte) (*AcceptPayload, error) {
	if len(b) < 10 {
		return nil, ErrPacketTooShort
	}
	p := &AcceptPayload{
		SessionID: binary.BigEndian.Uint32(b[0:4]),
		MTU:       binary.BigEndian.Uint16(b[8:10]),
	}
	copy(p.VirtualIP[:], b[4:8])
	return p, nil
}
