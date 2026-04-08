// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// Package handshake implements the HomeTunnel 4-message key exchange.
//
// Server flow: HELLO → CHALLENGE → AUTH → ACCEPT
// Client flow: HELLO → (wait CHALLENGE) → AUTH → (wait ACCEPT)
//
// After a successful handshake both sides have:
//   - A shared *encryption.Session
//   - An agreed SessionID
//   - (client only) An assigned virtual IP and MTU
package handshake

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"

	"github.com/d991d/hometunnel/core/encryption"
	"github.com/d991d/hometunnel/core/transport"
	"github.com/d991d/hometunnel/shared/protocol"
)

// HandshakeTimeout is how long either side waits for the next message.
const HandshakeTimeout = 15 * time.Second

// Errors
var (
	ErrTimeout          = errors.New("handshake: timed out")
	ErrInvalidChallenge = errors.New("handshake: invalid challenge response")
	ErrBadToken         = errors.New("handshake: invalid or expired token")
	ErrUnexpectedType   = errors.New("handshake: unexpected packet type")
)

// ─── Result types ─────────────────────────────────────────────────────────────

// ServerResult is returned to the server after a successful handshake.
type ServerResult struct {
	Session    *encryption.Session
	SessionID  uint32
	PeerAddr   *net.UDPAddr
	Token      string // decoded invite token string
	DisplayName string
}

// ClientResult is returned to the client after a successful handshake.
type ClientResult struct {
	Session   *encryption.Session
	SessionID uint32
	VirtualIP net.IP
	MTU       int
}

// ─── Token validator callback ─────────────────────────────────────────────────

// TokenValidator is called by the server to verify an invite token.
// It should return the peer's display name on success, or an error if invalid.
type TokenValidator func(token string) (displayName string, err error)

// ─── Server handshake ─────────────────────────────────────────────────────────

// ServerHandshake performs the server side of the handshake.
// It blocks until the handshake completes or ctx is cancelled.
//
//   - conn is the shared server UDP transport
//   - peerAddr is the remote address from the HELLO packet
//   - helloPayload is the raw payload of the HELLO packet
//   - validateToken is a callback that verifies the invite token
//   - assignIP assigns the next available virtual IP (returns 4-byte array)
func ServerHandshake(
	ctx context.Context,
	conn *transport.Conn,
	peerAddr *net.UDPAddr,
	helloRaw []byte,
	validateToken TokenValidator,
	assignIP func() ([4]byte, uint32, error),
) (*ServerResult, error) {

	// ── Step 1: decode HELLO ──────────────────────────────────────────────────
	hello, err := protocol.DecodeHello(helloRaw)
	if err != nil {
		return nil, err
	}

	// ── Step 2: generate server ephemeral key pair + challenge ────────────────
	serverKP, err := encryption.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	challenge, err := encryption.Random16()
	if err != nil {
		return nil, err
	}

	// ── Step 3: send CHALLENGE ────────────────────────────────────────────────
	cp := &protocol.ChallengePayload{
		ServerPubKey: serverKP.Public,
		Challenge:    challenge,
	}
	hdr := protocol.NewHeader(protocol.TypeChallenge, 0, 0)
	pkt := protocol.BuildPacket(hdr, protocol.EncodeChallenge(cp))
	conn.Send(pkt, peerAddr)

	// ── Step 4: derive session key ────────────────────────────────────────────
	sharedSecret, err := encryption.SharedSecret(serverKP.Private, hello.ClientPubKey)
	if err != nil {
		return nil, err
	}
	sessionKey, err := encryption.DeriveSessionKey(sharedSecret, challenge[:])
	if err != nil {
		return nil, err
	}

	// ── Step 5: wait for AUTH ─────────────────────────────────────────────────
	authRaw, err := waitForType(ctx, conn, peerAddr, protocol.TypeAuth)
	if err != nil {
		return nil, err
	}
	authPayload, err := protocol.DecodeAuth(authRaw)
	if err != nil {
		return nil, err
	}

	// ── Step 6: verify HMAC ───────────────────────────────────────────────────
	// Client computes: HMAC-SHA256(sessionKey, challenge + encryptedToken)
	mac := hmac.New(sha256.New, sessionKey[:])
	mac.Write(challenge[:])
	mac.Write(authPayload.EncryptedToken)
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, authPayload.HMAC[:]) {
		return nil, ErrInvalidChallenge
	}

	// ── Step 7: decrypt and validate token ────────────────────────────────────
	// Use sessionKey to decrypt the token (simple XOR-stream via ChaCha20)
	// We use sequence 0 for the auth message.
	tmpSession, err := encryption.NewSession(sessionKey, 0, encryption.CipherChaCha20Poly1305)
	if err != nil {
		return nil, err
	}
	tokenBytes, err := tmpSession.Open(authPayload.EncryptedToken, 0, challenge[:])
	if err != nil {
		return nil, ErrBadToken
	}
	tokenStr := string(tokenBytes)
	displayName, err := validateToken(tokenStr)
	if err != nil {
		return nil, ErrBadToken
	}

	// ── Step 8: assign session ID and virtual IP ──────────────────────────────
	virtualIP, sessionID, err := assignIP()
	if err != nil {
		return nil, err
	}

	// ── Step 9: send SESSION ACCEPT ───────────────────────────────────────────
	sess, err := encryption.NewSession(sessionKey, sessionID, encryption.CipherChaCha20Poly1305)
	if err != nil {
		return nil, err
	}
	ap := &protocol.AcceptPayload{
		SessionID: sessionID,
		VirtualIP: virtualIP,
		MTU:       1380,
	}
	acceptHdr := protocol.NewHeader(protocol.TypeAccept, sessionID, 0)
	acceptPkt := protocol.BuildPacket(acceptHdr, protocol.EncodeAccept(ap))
	conn.Send(acceptPkt, peerAddr)

	return &ServerResult{
		Session:     sess,
		SessionID:   sessionID,
		PeerAddr:    peerAddr,
		Token:       tokenStr,
		DisplayName: displayName,
	}, nil
}

// ─── Client handshake ─────────────────────────────────────────────────────────

// ClientHandshake performs the client side of the handshake.
// conn must be a connected (Dial) transport targeting the server.
// token is the raw invite token string.
func ClientHandshake(ctx context.Context, conn *transport.Conn, token string) (*ClientResult, error) {

	// ── Step 1: generate client ephemeral key pair ────────────────────────────
	clientKP, err := encryption.GenerateKeyPair()
	if err != nil {
		return nil, err
	}

	// ── Step 2: send HELLO ────────────────────────────────────────────────────
	hp := &protocol.HelloPayload{ClientPubKey: clientKP.Public}
	hdr := protocol.NewHeader(protocol.TypeHello, 0, 0)
	pkt := protocol.BuildPacket(hdr, protocol.EncodeHello(hp))
	conn.Send(pkt, nil)

	// ── Step 3: wait for CHALLENGE ────────────────────────────────────────────
	challengeRaw, err := waitForTypeClient(ctx, conn, protocol.TypeChallenge)
	if err != nil {
		return nil, err
	}
	cp, err := protocol.DecodeChallenge(challengeRaw)
	if err != nil {
		return nil, err
	}

	// ── Step 4: compute session key ───────────────────────────────────────────
	sharedSecret, err := encryption.SharedSecret(clientKP.Private, cp.ServerPubKey)
	if err != nil {
		return nil, err
	}
	sessionKey, err := encryption.DeriveSessionKey(sharedSecret, cp.Challenge[:])
	if err != nil {
		return nil, err
	}

	// ── Step 5: encrypt token with session key (seq 0) ────────────────────────
	tmpSession, err := encryption.NewSession(sessionKey, 0, encryption.CipherChaCha20Poly1305)
	if err != nil {
		return nil, err
	}
	encToken, _ := tmpSession.Seal([]byte(token), cp.Challenge[:])

	// ── Step 6: compute HMAC ──────────────────────────────────────────────────
	mac := hmac.New(sha256.New, sessionKey[:])
	mac.Write(cp.Challenge[:])
	mac.Write(encToken)
	var macArr [32]byte
	copy(macArr[:], mac.Sum(nil))

	// ── Step 7: send AUTH ─────────────────────────────────────────────────────
	authPayload := &protocol.AuthPayload{HMAC: macArr, EncryptedToken: encToken}
	authHdr := protocol.NewHeader(protocol.TypeAuth, 0, 0)
	authPkt := protocol.BuildPacket(authHdr, protocol.EncodeAuth(authPayload))
	conn.Send(authPkt, nil)

	// ── Step 8: wait for SESSION ACCEPT ───────────────────────────────────────
	acceptRaw, err := waitForTypeClient(ctx, conn, protocol.TypeAccept)
	if err != nil {
		return nil, err
	}
	ap, err := protocol.DecodeAccept(acceptRaw)
	if err != nil {
		return nil, err
	}

	sess, err := encryption.NewSession(sessionKey, ap.SessionID, encryption.CipherChaCha20Poly1305)
	if err != nil {
		return nil, err
	}

	return &ClientResult{
		Session:   sess,
		SessionID: ap.SessionID,
		VirtualIP: net.IP(ap.VirtualIP[:]),
		MTU:       int(ap.MTU),
	}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// waitForType reads from conn.Recv() until a packet of wantType arrives from peerAddr,
// or until ctx expires. Returns the raw payload (after the header).
func waitForType(ctx context.Context, conn *transport.Conn, peerAddr *net.UDPAddr, wantType uint8) ([]byte, error) {
	deadline := time.Now().Add(HandshakeTimeout)
	for {
		select {
		case <-ctx.Done():
			return nil, ErrTimeout
		case pkt, ok := <-conn.Recv():
			if !ok {
				return nil, errors.New("handshake: transport closed")
			}
			if pkt.Addr.String() != peerAddr.String() {
				continue
			}
			hdr, err := protocol.DecodeHeader(pkt.Data)
			if err != nil {
				continue
			}
			if hdr.Type != wantType {
				continue
			}
			return pkt.Data[protocol.HeaderSize:], nil
		default:
			if time.Now().After(deadline) {
				return nil, ErrTimeout
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// waitForTypeClient is a simplified version for connected client sockets
// where addr filtering is not needed.
func waitForTypeClient(ctx context.Context, conn *transport.Conn, wantType uint8) ([]byte, error) {
	deadline := time.Now().Add(HandshakeTimeout)
	for {
		select {
		case <-ctx.Done():
			return nil, ErrTimeout
		case pkt, ok := <-conn.Recv():
			if !ok {
				return nil, errors.New("handshake: transport closed")
			}
			hdr, err := protocol.DecodeHeader(pkt.Data)
			if err != nil {
				continue
			}
			if hdr.Type != wantType {
				continue
			}
			return pkt.Data[protocol.HeaderSize:], nil
		default:
			if time.Now().After(deadline) {
				return nil, ErrTimeout
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// randomSessionID generates a cryptographically random 32-bit session ID.
func randomSessionID() (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}
