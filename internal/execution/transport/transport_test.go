package transport

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestWebSocketUpgradeAfterHTTP2REST(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("/stream", websocket.Handler(func(conn *websocket.Conn) { _, _ = io.Copy(conn, conn) }))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	transport, err := New(server.URL, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.HTTP.CloseIdleConnections()
	response, err := transport.HTTP.Get(server.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatal("test did not exercise HTTP/2")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, "/stream", http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var out [5]byte
	if _, err = io.ReadFull(conn, out[:]); err != nil || string(out[:]) != "hello" {
		t.Fatalf("stream: %q %v", out, err)
	}
}
