// Package push sends Web Push notifications: the message encryption of RFC 8291
// and the VAPID identification of RFC 8292, over the standard library alone.
//
// A notification goes to the push service the browser chose (Google's for
// Chrome, Mozilla's for Firefox, Apple's for Safari), which wakes the service
// worker of the installed client. The payload is encrypted for that browser
// only: the push service relays bytes it cannot read.
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// recordSize is the single record a notification fits in. Push services
// accept 4096 bytes of payload; a notification is a title, a line and a link.
const recordSize = 4096

// ErrGone means the subscription no longer exists: the browser unsubscribed
// or the person cleared the site's data. It should be forgotten.
var ErrGone = errors.New("the push subscription is gone")

// Subscription is what a browser hands out when it subscribes.
type Subscription struct {
	Endpoint string
	// P256DH is the browser's public key, uncompressed, 65 bytes.
	P256DH []byte
	// Auth is the 16-byte shared authentication secret.
	Auth []byte
}

// Keys is the server's VAPID identity: one P-256 key pair for every
// subscription, whose public half the browser was given when subscribing.
type Keys struct {
	private *ecdsa.PrivateKey
}

// GenerateKeys makes a new VAPID identity.
func GenerateKeys() (Keys, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Keys{}, err
	}
	return Keys{private: key}, nil
}

// ParseKeys reads an identity saved with Keys.Private.
func ParseKeys(private []byte) (Keys, error) {
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), private)
	if err != nil {
		return Keys{}, fmt.Errorf("read the VAPID key: %w", err)
	}
	return Keys{private: key}, nil
}

// Private is the raw private key, to keep.
func (k Keys) Private() []byte {
	raw, _ := k.private.Bytes()
	return raw
}

// Public is the uncompressed public key, base64url without padding: what a
// browser takes as applicationServerKey.
func (k Keys) Public() string {
	raw, _ := k.private.PublicKey.Bytes()
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Message is one notification.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// URL is where a tap on the notification leads, relative to the client.
	URL string `json:"url"`
	// Tag groups notifications: a new one with the same tag replaces the
	// previous one rather than piling up.
	Tag string `json:"tag,omitempty"`
}

// Sender delivers notifications.
type Sender struct {
	Keys   Keys
	Client *http.Client
	// Subject identifies the sender to push services, a mailto: or https: URL.
	Subject string
}

// Send encrypts and delivers one message. ErrGone means the subscription
// should be deleted.
func (s Sender) Send(ctx context.Context, sub Subscription, msg Message) error {
	endpoint, err := url.Parse(sub.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return fmt.Errorf("the push endpoint is not an https URL")
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	body, err := encrypt(payload, sub, ephemeral, salt)
	if err != nil {
		return err
	}
	token, err := s.vapid(endpoint.Scheme + "://" + endpoint.Host)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	// A day: a question asked overnight is still worth seeing in the morning.
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", "vapid t="+token+", k="+s.Keys.Public())

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("deliver to the push service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("the push service answered %s", resp.Status)
	}
	return nil
}

// vapid signs the JWT that identifies this server to one push service.
func (s Sender) vapid(audience string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": audience,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": s.Subject,
	})
	if err != nil {
		return "", err
	}
	signed := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	r, sig, err := ecdsa.Sign(rand.Reader, s.Keys.private, digest[:])
	if err != nil {
		return "", err
	}
	// JWS wants r and s as two fixed 32-byte halves, not DER.
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	sig.FillBytes(raw[32:])
	return signed + "." + base64.RawURLEncoding.EncodeToString(raw), nil
}

// encrypt is RFC 8291: an ephemeral ECDH exchange with the browser's key,
// mixed with its auth secret, keys one aes128gcm record (RFC 8188). The
// ephemeral key and the salt are fresh for every message; they are parameters
// so the RFC's own example can pin the result.
func encrypt(plaintext []byte, sub Subscription, ephemeral *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext)+1+16 > recordSize-86 {
		return nil, fmt.Errorf("the notification is too long to send")
	}
	browser, err := ecdh.P256().NewPublicKey(sub.P256DH)
	if err != nil {
		return nil, fmt.Errorf("the subscription key is not a P-256 point: %w", err)
	}
	if len(sub.Auth) != 16 {
		return nil, fmt.Errorf("the subscription auth secret is not 16 bytes")
	}
	if len(salt) != 16 {
		return nil, fmt.Errorf("the salt is not 16 bytes")
	}

	shared, err := ephemeral.ECDH(browser)
	if err != nil {
		return nil, err
	}
	server := ephemeral.PublicKey().Bytes()

	info := append(append([]byte("WebPush: info\x00"), sub.P256DH...), server...)
	ikm, err := hkdf.Key(sha256.New, shared, sub.Auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record, so it is the last: the delimiter is 0x02, and no padding.
	sealed := gcm.Seal(nil, nonce, append(append([]byte{}, plaintext...), 0x02), nil)

	header := make([]byte, 0, 16+4+1+len(server))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(server)))
	header = append(header, server...)
	return append(header, sealed...), nil
}

// DecodeKey reads a key a browser sent, in either base64 alphabet, padded or
// not: PushSubscription.toJSON uses base64url, and not every client agrees.
func DecodeKey(text string) ([]byte, error) {
	text = strings.TrimRight(text, "=")
	text = strings.NewReplacer("+", "-", "/", "_").Replace(text)
	return base64.RawURLEncoding.DecodeString(text)
}
