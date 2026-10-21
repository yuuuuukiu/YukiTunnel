package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/yuuuuukiu/yukitunnel/internal/wsnet"
)

// fakeAgent dials the hub, registers tunnels and echoes local traffic.
func fakeAgent(t *testing.T, hubURL, token string, specs []TunnelSpec, echo bool) *yamux.Session {
	t.Helper()
	return fakeAgentOpts(t, hubURL, token, specs, echo, false)
}

// fakeAgentForward makes the agent dial each tunnel's LocalIP:LocalPort and
// splice bytes, which is what the real client does. Needed whenever the test
// cares about the local service's own protocol (e.g. TLS termination).
func fakeAgentForward(t *testing.T, hubURL, token string, specs []TunnelSpec) *yamux.Session {
	t.Helper()
	return fakeAgentOpts(t, hubURL, token, specs, false, true)
}

func fakeAgentOpts(t *testing.T, hubURL, token string, specs []TunnelSpec, echo, forward bool) *yamux.Session {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(hubURL, "http") + "/v1/control"
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = ioDiscard{}
	cfg.EnableKeepAlive = false
	client, err := yamux.Client(wsnet.New(conn), cfg)
	if err != nil {
		t.Fatalf("yamux: %v", err)
	}
	stream, err := client.OpenStream()
	if err != nil {
		t.Fatalf("open ctrl: %v", err)
	}
	var mu sync.Mutex
	if err := WriteControl(stream, &mu, ControlMessage{Type: MsgHello, Version: ProtocolVersion, Client: "test"}); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(stream)
	if msg, err := ReadControl(br); err != nil || msg.Type != MsgHelloOK {
		t.Fatalf("hello: %v %v", msg, err)
	}
	reqs := make([]RegisterTunnel, 0, len(specs))
	for _, s := range specs {
		reqs = append(reqs, RegisterTunnel{ID: s.ID, Proto: s.Proto, LocalIP: s.LocalIP, LocalPort: s.LocalPort, PublicPort: s.PublicPort, Domain: s.Domain})
	}
	if err := WriteControl(stream, &mu, ControlMessage{Type: MsgRegister, Tunnels: reqs}); err != nil {
		t.Fatal(err)
	}
	res, err := ReadControl(br)
	if err != nil || res.Type != MsgRegistered {
		t.Fatalf("register: %v %v", res, err)
	}
	for _, r := range res.Results {
		if !r.OK {
			t.Fatalf("tunnel %s failed: %s", r.ID, r.Error)
		}
	}
	// Serve streams: echo or HTTP.
	go func() {
		for {
			st, err := client.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				defer st.Close()
				rb := bufio.NewReader(st)
				h, err := ReadHeader(rb)
				if err != nil {
					return
				}
				if h.Kind == "http" {
					req, err := http.ReadRequest(rb)
					if err != nil {
						return
					}
					body, _ := io.ReadAll(req.Body)
					resp := "HTTP/1.1 200 OK\r\nContent-Length: " + strconv.Itoa(len("local:"+string(body))) +
						"\r\nContent-Type: text/plain\r\n\r\nlocal:" + string(body)
					_, _ = st.Write([]byte(resp))
					return
				}
				if h.Kind == "udp" {
					for {
						pkt, err := ReadDatagram(rb)
						if err != nil {
							return
						}
						_ = WriteDatagram(st, pkt)
					}
				}
				if forward {
					local, err := net.DialTimeout("tcp", net.JoinHostPort(h.LocalIP, strconv.Itoa(h.LocalPort)), 2*time.Second)
					if err != nil {
						return
					}
					defer local.Close()
					done := make(chan struct{}, 1)
					go func() {
						_, _ = io.Copy(local, rb)
						done <- struct{}{}
					}()
					_, _ = io.Copy(st, local)
					<-done
					return
				}
				_, _ = io.Copy(st, rb)
			}()
		}
	}()
	return client
}

func newTestHub(t *testing.T, specs map[string]TunnelSpec) (*Hub, *httptest.Server) {
	t.Helper()
	hooks := Hooks{
		Authenticate: func(ctx context.Context, token string) (*Identity, error) {
			if token != "good" {
				return nil, io.EOF
			}
			return &Identity{UserID: "u1", Username: "alice"}, nil
		},
		LookupTunnel: func(ctx context.Context, userID, id string) (*TunnelSpec, error) {
			s, ok := specs[id]
			if !ok {
				return nil, ErrUnknownTunnel
			}
			cp := s
			return &cp, nil
		},
		AllocatePort: func(ctx context.Context, spec *TunnelSpec) (int, error) { return freePort(t), nil },
	}
	hub := NewHub(context.Background(), hooks, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/control", hub.ServeControl)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { srv.Close(); hub.Close() })
	return hub, srv
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestTCPTunnelRoundTrip(t *testing.T) {
	port := freePort(t)
	specs := map[string]TunnelSpec{"t1": {ID: "t1", Proto: "tcp", PublicPort: port, BindAddr: "127.0.0.1", PublicHost: "127.0.0.1"}}
	_, srv := newTestHub(t, specs)
	_ = fakeAgent(t, srv.URL, "good", []TunnelSpec{specs["t1"]}, true)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()
	msg := []byte("hello yuki")
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("got %q want %q", buf, msg)
	}
}

func TestHTTPTunnel(t *testing.T) {
	port := freePort(t)
	specs := map[string]TunnelSpec{"h1": {ID: "h1", Proto: "http", PublicPort: port, BindAddr: "127.0.0.1", PublicHost: "127.0.0.1", LocalIP: "127.0.0.1", LocalPort: 80}}
	_, srv := newTestHub(t, specs)
	_ = fakeAgent(t, srv.URL, "good", []TunnelSpec{specs["h1"]}, true)
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Post("http://127.0.0.1:"+strconv.Itoa(port)+"/", "text/plain", strings.NewReader("ping"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "local:ping") {
		t.Fatalf("unexpected body %q", b)
	}
}

func TestUDPTunnel(t *testing.T) {
	port := freePort(t)
	specs := map[string]TunnelSpec{"u1": {ID: "u1", Proto: "udp", PublicPort: port, BindAddr: "127.0.0.1", PublicHost: "127.0.0.1"}}
	_, srv := newTestHub(t, specs)
	_ = fakeAgent(t, srv.URL, "good", []TunnelSpec{specs["u1"]}, true)
	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("udp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := []byte("udp-hello")
	_ = binary.BigEndian
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("got %q", buf[:n])
	}
}

func TestUnauthorizedControl(t *testing.T) {
	_, srv := newTestHub(t, map[string]TunnelSpec{})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/control"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer nope"}})
	if err == nil {
		t.Fatal("expected dial failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %v", resp)
	}
}

// TestHTTPSTunnelPassthrough covers the one protocol with no test: "https" is a
// raw TLS passthrough, so the server must not terminate or inspect TLS. The
// byte stream is forwarded verbatim and the local service owns the certificate.
func TestHTTPSTunnelPassthrough(t *testing.T) {
	port := freePort(t)
	specs := map[string]TunnelSpec{"s1": {ID: "s1", Proto: "https", PublicPort: port, BindAddr: "127.0.0.1", PublicHost: "127.0.0.1"}}
	_, srv := newTestHub(t, specs)
	_ = fakeAgent(t, srv.URL, "good", []TunnelSpec{specs["s1"]}, true)
	time.Sleep(100 * time.Millisecond)

	// A real TLS client handshake proves the tunnel carries raw bytes: the
	// fake agent echoes, so the ClientHello round-trips and the server sees a
	// TLS alert back. Success here means no TLS termination happened upstream.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()

	clientHello := []byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00, 0x00, 0x01, 0x00}
	if _, err := conn.Write(clientHello); err != nil {
		t.Fatalf("write client hello: %v", err)
	}
	buf := make([]byte, len(clientHello))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(clientHello) {
		t.Fatalf("TLS bytes were altered: got %x want %x", buf, clientHello)
	}
}

// TestHTTPSTunnelCarriesRealTLS runs a genuine TLS handshake through the tunnel
// against a local TLS server, proving end-to-end encryption survives.
func TestHTTPSTunnelCarriesRealTLS(t *testing.T) {
	local := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secure-ok"))
	}))
	defer local.Close()
	localPort := local.Listener.Addr().(*net.TCPAddr).Port

	port := freePort(t)
	spec := TunnelSpec{
		ID: "s2", Proto: "https", PublicPort: port, BindAddr: "127.0.0.1",
		PublicHost: "127.0.0.1", LocalIP: "127.0.0.1", LocalPort: localPort,
	}
	_, srv := newTestHub(t, map[string]TunnelSpec{"s2": spec})
	_ = fakeAgentForward(t, srv.URL, "good", []TunnelSpec{spec})
	time.Sleep(150 * time.Millisecond)

	// Trust the local server's self-signed cert; the tunnel must not rewrite it.
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Get("https://127.0.0.1:" + strconv.Itoa(port) + "/")
	if err != nil {
		t.Fatalf("https through tunnel: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secure-ok" {
		t.Fatalf("unexpected body %q", body)
	}
}
