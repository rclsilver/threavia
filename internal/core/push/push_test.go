package push

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func b64(t *testing.T, text string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		t.Fatalf("decoding %q: %v", text, err)
	}
	return raw
}

// TestEncryptionMatchesRFC8291 pins the encryption to the worked example of
// RFC 8291, appendix A: if a byte differs, no browser could read it.
func TestEncryptionMatchesRFC8291(t *testing.T) {
	t.Parallel()

	ephemeral, err := ecdh.P256().NewPrivateKey(b64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	sub := Subscription{
		P256DH: b64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"),
		Auth:   b64(t, "BTBZMqHH6r4Tts7J_aSIgg"),
	}
	got, err := encrypt([]byte("When I grow up, I want to be a watermelon"), sub, ephemeral,
		b64(t, "DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if encoded := base64.RawURLEncoding.EncodeToString(got); encoded != want {
		t.Fatalf("encrypted =\n%s\nwant\n%s", encoded, want)
	}
}

// TestSendIdentifiesItselfAndReportsAGoneSubscription pins the request a push
// service receives: an encrypted body, a VAPID token it can verify with the
// key the browser was given, and a 410 understood as "forget it".
func TestSendIdentifiesItselfAndReportsAGoneSubscription(t *testing.T) {
	t.Parallel()

	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ParseKeys(keys.Private())
	if err != nil || reloaded.Public() != keys.Public() {
		t.Fatalf("a saved key must read back as itself: %v", err)
	}

	browser, err := ecdh.P256().GenerateKey(randReader{})
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)

	gone := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("content encoding = %q", r.Header.Get("Content-Encoding"))
		}
		authorization := r.Header.Get("Authorization")
		token, key, ok := strings.Cut(strings.TrimPrefix(authorization, "vapid t="), ", k=")
		if !ok || key != keys.Public() {
			t.Errorf("authorization = %q, want the token and the public key", authorization)
		}
		verifyJWT(t, token, key, "https://"+r.Host)
		body, _ := io.ReadAll(r.Body)
		if len(body) < 86 {
			t.Errorf("body of %d bytes cannot hold the header and a record", len(body))
		}
		if gone {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	sender := Sender{Keys: keys, Client: server.Client(), Subject: "https://threavia.example"}
	sub := Subscription{Endpoint: server.URL + "/push/abc", P256DH: browser.PublicKey().Bytes(), Auth: auth}
	if err := sender.Send(context.Background(), sub, Message{Title: "t", Body: "b", URL: "/"}); err != nil {
		t.Fatalf("sending: %v", err)
	}
	gone = true
	if err := sender.Send(context.Background(), sub, Message{Title: "t"}); err != ErrGone {
		t.Fatalf("a 410 = %v, want ErrGone", err)
	}
	if err := sender.Send(context.Background(), Subscription{Endpoint: "http://insecure/x"}, Message{}); err == nil {
		t.Fatal("a plain http endpoint must be refused")
	}
}

type randReader struct{}

func (randReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i*7 + 13)
	}
	return len(p), nil
}

func verifyJWT(t *testing.T, token, key, audience string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q is not a JWT", token)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(b64(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != audience || claims.Sub == "" || claims.Exp == 0 {
		t.Errorf("claims = %+v, want aud %s and a subject", claims, audience)
	}
	raw := b64(t, key)
	x, y := new(big.Int).SetBytes(raw[1:33]), new(big.Int).SetBytes(raw[33:])
	public := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	signature := b64(t, parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(public, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		t.Error("the VAPID signature does not verify with the public key")
	}
}
