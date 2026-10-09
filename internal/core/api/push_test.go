package api_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// pushService stands in for a browser vendor's push service, and for the
// browser behind it: it holds the subscription keys, so it can decrypt what
// Core sent and check it says the right thing.
type pushService struct {
	server  *httptest.Server
	browser *ecdh.PrivateKey
	auth    []byte

	mu       sync.Mutex
	received []map[string]string
	gone     bool
}

func newPushService(t *testing.T) *pushService {
	t.Helper()
	browser, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := &pushService{browser: browser, auth: make([]byte, 16)}
	_, _ = rand.Read(p.auth)
	p.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		message, err := p.decrypt(body)
		if err != nil {
			t.Errorf("a notification the browser cannot read: %v", err)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.received = append(p.received, message)
		if p.gone {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *pushService) messages() []map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]string(nil), p.received...)
}

// decrypt is RFC 8291 from the browser's side.
func (p *pushService) decrypt(body []byte) (map[string]string, error) {
	salt, keyLength := body[:16], int(body[20])
	server, err := ecdh.P256().NewPublicKey(body[21 : 21+keyLength])
	if err != nil {
		return nil, err
	}
	shared, err := p.browser.ECDH(server)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), p.browser.PublicKey().Bytes()...), server.Bytes()...)
	ikm, _ := hkdf.Key(sha256.New, shared, p.auth, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+keyLength:], nil)
	if err != nil {
		return nil, err
	}
	var message map[string]string
	return message, json.Unmarshal(plain[:len(plain)-1], &message)
}

// TestABrowserIsNotifiedOfWorkNobodyIsWatching pins the Web Push path end to
// end: a browser subscribes with the key Core publishes, the end of a Job
// nobody is watching reaches it encrypted for that browser alone, and a
// subscription the push service says is gone is forgotten.
func TestABrowserIsNotifiedOfWorkNobodyIsWatching(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	service := newPushService(t)
	c.svc.SetPushClient(service.server.Client())

	var config struct {
		PublicKey     string `json:"publicKey"`
		Subscriptions []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"subscriptions"`
	}
	c.mustDo(http.MethodGet, "/api/v1/me/push", nil, &config, http.StatusOK)
	if key, err := base64.RawURLEncoding.DecodeString(config.PublicKey); err != nil || len(key) != 65 {
		t.Fatalf("public key %q is not an uncompressed P-256 point", config.PublicKey)
	}

	c.mustDo(http.MethodPost, "/api/v1/me/push/subscriptions", map[string]any{
		"endpoint": service.server.URL + "/push/device-1",
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(service.browser.PublicKey().Bytes()),
			"auth":   base64.RawURLEncoding.EncodeToString(service.auth),
		},
		"label": "Android — Chrome",
	}, nil, http.StatusCreated, [2]string{"Origin", "https://threavia.example"},
		// The browser that sends this Origin says the request is its own page's,
		// which is what lets it through when Core is reached under another name.
		[2]string{"Sec-Fetch-Site", "same-origin"})
	c.mustDo(http.MethodPost, "/api/v1/me/push/subscriptions", map[string]any{
		"endpoint": "http://not-https.example/push", "keys": map[string]string{"p256dh": "x", "auth": "y"},
	}, nil, http.StatusBadRequest)

	c.mustDo(http.MethodPost, "/api/v1/me/push/test", nil, nil, http.StatusAccepted)
	waitUntil(t, "the test notification", func() bool { return len(service.messages()) == 1 })

	// The person sits at the desk: the desktop client is open and active.
	desk := map[string]string{
		"X-Threavia-Channel":     "web",
		"X-Threavia-Client":      "desk-1",
		"X-Threavia-Client-Name": "workstation%20%E2%80%94%20Firefox",
		"X-Threavia-Active":      "true",
	}
	stop := c.watchWith(desk)
	t.Cleanup(stop)
	var clients struct {
		Items []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Active bool   `json:"active"`
		} `json:"items"`
	}
	waitUntil(t, "the desk to be connected", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/me/clients", nil, &clients, http.StatusOK)
		return len(clients.Items) == 1
	})
	if clients.Items[0].Name != "workstation — Firefox" || !clients.Items[0].Active {
		t.Fatalf("clients = %+v, want the named, active desk", clients.Items)
	}

	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	local := newWorkingRunner()
	t.Cleanup(local.stop)
	c.connectBackendWith(local, credential, t.TempDir())
	session := c.startSessionFrom(project, backendID, "web", "check the backups")
	receive(t, "the job to start", local.started)
	local.stop()

	jobsDone := func(n int) func() bool {
		return func() bool {
			var snapshot struct {
				Jobs []struct {
					Status string `json:"status"`
				} `json:"jobs"`
			}
			c.mustDo(http.MethodGet, "/api/v1/sessions/"+session, nil, &snapshot, http.StatusOK)
			done := 0
			for _, job := range snapshot.Jobs {
				if job.Status == "COMPLETED" {
					done++
				}
			}
			return done == n
		}
	}
	waitUntil(t, "the first job to end", jobsDone(1))
	if got := len(service.messages()); got != 1 {
		t.Fatalf("%d notifications while the person was at the desk, want only the test one", got)
	}

	// The desk goes idle, as when its person walks away: the phone rings.
	c.mustDo(http.MethodPost, "/api/v1/me/presence", map[string]bool{"active": false}, nil,
		http.StatusNoContent, [2]string{"X-Threavia-Client", "desk-1"})
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages",
		map[string]string{"message": "and the restore test"}, nil, http.StatusCreated)
	waitUntil(t, "the notification of the end of the job", func() bool { return len(service.messages()) == 2 })
	done := service.messages()[1]
	if done["url"] != "/sessions/"+session || done["tag"] != session || done["title"] == "" {
		t.Fatalf("notification = %v, want a title and a link to the session", done)
	}

	// The browser cleared its data: the next delivery learns it and forgets.
	service.mu.Lock()
	service.gone = true
	service.mu.Unlock()
	c.mustDo(http.MethodPost, "/api/v1/me/push/test", nil, nil, http.StatusAccepted)
	waitUntil(t, "the gone subscription to be forgotten", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/me/push", nil, &config, http.StatusOK)
		return len(config.Subscriptions) == 0
	})
}
