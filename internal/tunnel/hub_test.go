package tunnel

import (
	"bufio"
	"context"
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
