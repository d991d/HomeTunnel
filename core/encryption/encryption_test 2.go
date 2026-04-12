// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — encryption unit tests

package encryption

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// ── KeyPair & SharedSecret ────────────────────────────────────────────────────

func TestGenerateKeyPair(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	var zero [32]byte
	if kp.Public == zero {
		t.Fatal("public key is all-zero")
	}
	if kp.Private == zero {
		t.Fatal("private key is all-zero")
	}
}

func TestSharedSecretSymmetric(t *testing.T) {
	a, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair A: %v", err)
	}
	b, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair B: %v", err)
	}

	ssAB, err := SharedSecret(a.Private, b.Public)
	if err != nil {
		t.Fatalf("SharedSecret A→B: %v", err)
	}
	ssBA, err := SharedSecret(b.Private, a.Public)
	if err != nil {
		t.Fatalf("SharedSecret B→A: %v", err)
	}
	if ssAB != ssBA {
		t.Fatal("shared secrets do not match — ECDH asymmetry bug")
	}
}

func TestSharedSecretUnique(t *testing.T) {
	a, _ := GenerateKeyPair()
	b, _ := GenerateKeyPair()
	c, _ := GenerateKeyPair()

	ss1, _ := SharedSecret(a.Private, b.Public)
	ss2, _ := SharedSecret(a.Private, c.Public)
	if ss1 == ss2 {
		t.Fatal("different peers produced identical shared secret")
	}
}

// ── DeriveSessionKey ─────────────────────────────────────────────────────────

func TestDeriveSessionKeyDeterministic(t *testing.T) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	salt := []byte("test-salt-12345678")

	k1, err := DeriveSessionKey(secret, salt)
	if err != nil {
		t.Fatalf("DeriveSessionKey: %v", err)
	}
	k2, err := DeriveSessionKey(secret, salt)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Fatal("DeriveSessionKey is not deterministic")
	}
}

func TestDeriveSessionKeyDifferentSalts(t *testing.T) {
	var secret [32]byte
	rand.Read(secret[:])

	k1, _ := DeriveSessionKey(secret, []byte("salt-a"))
	k2, _ := DeriveSessionKey(secret, []byte("salt-b"))
	if k1 == k2 {
		t.Fatal("different salts produced same session key")
	}
}

// ── Session (AEAD Seal / Open) ────────────────────────────────────────────────

func newTestSession(t *testing.T) *Session {
	t.Helper()
	var key [KeySize]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	// CipherID 0 = ChaCha20-Poly1305
	s, err := NewSession(key, 0xdeadbeef, 0)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s
}

func TestSealOpen(t *testing.T) {
	s := newTestSession(t)

	plaintext := []byte("Hello, HomeTunnel!")
	ad := []byte("additional-data")

	ct, seqNum := s.Seal(plaintext, ad)
	if bytes.Equal(ct, plaintext) {
		t.Fatal("ciphertext equals plaintext — encryption not applied")
	}

	pt, err := s.Open(ct, seqNum, ad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatalf("decrypted %q != original %q", pt, plaintext)
	}
}

func TestOpenRejectsWrongAD(t *testing.T) {
	s := newTestSession(t)
	ct, seqNum := s.Seal([]byte("secret"), []byte("good-ad"))
	_, err := s.Open(ct, seqNum, []byte("bad-ad"))
	if err == nil {
		t.Fatal("Open accepted tampered additional data")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	s := newTestSession(t)
	ct, seqNum := s.Seal([]byte("secret"), nil)
	ct[len(ct)/2] ^= 0xff
	_, err := s.Open(ct, seqNum, nil)
	if err == nil {
		t.Fatal("Open accepted corrupted ciphertext")
	}
}

func TestSealEmptyPlaintext(t *testing.T) {
	s := newTestSession(t)
	ct, seqNum := s.Seal([]byte{}, nil)
	pt, err := s.Open(ct, seqNum, nil)
	if err != nil {
		t.Fatalf("Open empty: %v", err)
	}
	if len(pt) != 0 {
		t.Fatalf("expected empty plaintext, got %d bytes", len(pt))
	}
}

func TestSealIncrementsSeqNum(t *testing.T) {
	s := newTestSession(t)
	_, seq1 := s.Seal([]byte("a"), nil)
	_, seq2 := s.Seal([]byte("b"), nil)
	if seq2 <= seq1 {
		t.Fatalf("sequence number did not increment: %d → %d", seq1, seq2)
	}
}

// ── ReplayWindow ─────────────────────────────────────────────────────────────

func TestReplayWindowAcceptsNew(t *testing.T) {
	rw := NewReplayWindow(256)
	for seqNum := uint64(1); seqNum <= 10; seqNum++ {
		if err := rw.Check(seqNum); err != nil {
			t.Fatalf("Check(%d) returned error for new seq: %v", seqNum, err)
		}
		rw.Mark(seqNum)
	}
}

func TestReplayWindowRejectsDuplicate(t *testing.T) {
	rw := NewReplayWindow(256)
	rw.Mark(42)
	if err := rw.Check(42); err == nil {
		t.Fatal("Check(42) should return error after Mark(42)")
	}
}

func TestReplayWindowRejectsOld(t *testing.T) {
	rw := NewReplayWindow(256)
	for i := uint64(1); i <= 300; i++ {
		rw.Mark(i)
	}
	// Seq 1 is far outside window of 256 from 300
	if err := rw.Check(1); err == nil {
		t.Fatal("Check(1) should return error — outside sliding window")
	}
}

func TestReplayWindowAcceptsOutOfOrderWithinWindow(t *testing.T) {
	rw := NewReplayWindow(256)
	rw.Mark(100)
	// 99 is within window and not yet marked
	if err := rw.Check(99); err != nil {
		t.Fatalf("Check(99) should return nil — within window and not seen: %v", err)
	}
}

func TestReplayWindowRejectsAlreadyMarkedInWindow(t *testing.T) {
	rw := NewReplayWindow(256)
	for i := uint64(1); i <= 300; i++ {
		rw.Mark(i)
	}
	// 290 is within window but was marked
	if err := rw.Check(290); err == nil {
		t.Fatal("Check(290) should be error — already marked")
	}
}

// ── HMAC ─────────────────────────────────────────────────────────────────────

func TestHMACSignVerify(t *testing.T) {
	key := []byte("test-hmac-key-32bytes-padding-ok!")
	msg := []byte("authenticate this message")

	sig := HMACSign(key, msg)
	if len(sig) == 0 {
		t.Fatal("HMACSign returned empty signature")
	}
	if !HMACVerify(key, msg, sig) {
		t.Fatal("HMACVerify rejected valid signature")
	}
}

func TestHMACVerifyRejectsTamperedMessage(t *testing.T) {
	key := []byte("test-hmac-key-32bytes-padding-ok!")
	msg := []byte("original message")
	sig := HMACSign(key, msg)

	tampered := []byte("tampered message")
	if HMACVerify(key, tampered, sig) {
		t.Fatal("HMACVerify accepted signature for different message")
	}
}

func TestHMACVerifyRejectsWrongKey(t *testing.T) {
	msg := []byte("message")
	sig := HMACSign([]byte("key-a"), msg)
	if HMACVerify([]byte("key-b"), msg, sig) {
		t.Fatal("HMACVerify accepted signature with wrong key")
	}
}

// ── DeriveNonce ───────────────────────────────────────────────────────────────

func TestDeriveNonceDeterministic(t *testing.T) {
	n1 := DeriveNonce(0xdeadbeef, 42)
	n2 := DeriveNonce(0xdeadbeef, 42)
	if n1 != n2 {
		t.Fatal("DeriveNonce is not deterministic")
	}
}

func TestDeriveNonceUnique(t *testing.T) {
	n1 := DeriveNonce(1, 1)
	n2 := DeriveNonce(1, 2)
	n3 := DeriveNonce(2, 1)
	if n1 == n2 {
		t.Fatal("different seqNum produced same nonce")
	}
	if n1 == n3 {
		t.Fatal("different sessionID produced same nonce")
	}
}

// ── RandomBytes ──────────────────────────────────────────────────────────────

func TestRandomBytesLength(t *testing.T) {
	for _, n := range []int{16, 32, 64} {
		b, err := RandomBytes(n)
		if err != nil {
			t.Fatalf("RandomBytes(%d): %v", n, err)
		}
		if len(b) != n {
			t.Fatalf("RandomBytes(%d) returned %d bytes", n, len(b))
		}
	}
}

func TestRandomBytesNotAllZero(t *testing.T) {
	b, _ := RandomBytes(32)
	allZero := true
	for _, v := range b {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("RandomBytes returned all-zero slice")
	}
}
