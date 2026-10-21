package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/yuuuuukiu/yukitunnel/internal/wsnet"
)

// TunnelSpec is the server's authoritative view of a tunnel.
type TunnelSpec struct {
	ID             string
	UserID         string
	NodeID         string
	Name           string
	Proto          string // tcp | udp | http | https
	LocalIP        string
	LocalPort      int
	PublicPort     int
	Domain         string
	BandwidthKbps  int
	MaxConnections int
	PublicHost     string // advertised host (IP or domain)
	BindAddr       string // listen address, usually 0.0.0.0
}

// Address returns the human-facing public address.
func (t TunnelSpec) Address() string {
	host := t.PublicHost
	if t.Domain != "" {
		host = t.Domain
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(t.PublicPort))
}

// Identity is the authenticated owner of a control session.
type Identity struct {
	UserID   string
	Username string
	IsAdmin  bool
}

// Hooks connects the Hub to a control plane (embedded store or remote API).
type Hooks struct {
	// Authenticate resolves a bearer token into an identity.
	Authenticate func(ctx context.Context, token string) (*Identity, error)
	// LookupTunnel returns the tunnel spec if the user may run it.
	LookupTunnel func(ctx context.Context, userID, tunnelID string) (*TunnelSpec, error)
	// ListRunnable returns every tunnel the user is allowed to start (used on
	// reconnect to restore state).
	ListRunnable func(ctx context.Context, userID string) ([]TunnelSpec, error)
	// AllocatePort picks a free public port when the spec has none.
	AllocatePort func(ctx context.Context, spec *TunnelSpec) (int, error)
	// SavePort persists the allocated port.
	SavePort func(ctx context.Context, tunnelID string, port int) error
	// OnSessionUp runs after a control session authenticates and before it
	// starts accepting streams; used to restore desired-running tunnels.
	OnSessionUp func(ctx context.Context, userID string)
	// Stats is called periodically with per-tunnel counters.
	Stats func(tunnelID string, rx, tx int64, conns int64, active int)
	// Log receives structured runtime events for the audit/UI log.
	Log func(level, msg string, kv map[string]any)
}

// Hub owns all live control sessions and public listeners.
type Hub struct {
	hooks  Hooks
	log    *slog.Logger
	upg    websocket.Upgrader
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.RWMutex
	sessions map[string]*clientSession // by user id
	tunnels  map[string]*runtimeTunnel // by tunnel id
	ports    map[int]string            // public port -> tunnel id (this process)
}

// NewHub builds a Hub. ctx bounds the lifetime of all listeners.
func NewHub(ctx context.Context, hooks Hooks, log *slog.Logger) *Hub {
	c, cancel := context.WithCancel(ctx)
	if log == nil {
		log = slog.Default()
	}
	h := &Hub{
		hooks:    hooks,
		log:      log,
		ctx:      c,
		cancel:   cancel,
		sessions: map[string]*clientSession{},
		tunnels:  map[string]*runtimeTunnel{},
		ports:    map[int]string{},
	}
	h.upg = websocket.Upgrader{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
	go h.statsLoop()
	return h
}

// Close stops every listener and session. Sessions are closed outside the hub
// lock because close() calls back into removeTunnel.
func (h *Hub) Close() {
	h.cancel()
	h.mu.Lock()
	tuns := make([]*runtimeTunnel, 0, len(h.tunnels))
	for id, rt := range h.tunnels {
		tuns = append(tuns, rt)
		delete(h.tunnels, id)
		delete(h.ports, rt.spec.PublicPort)
	}
	sessions := make([]*clientSession, 0, len(h.sessions))
	for id, s := range h.sessions {
		sessions = append(sessions, s)
		delete(h.sessions, id)
	}
	h.mu.Unlock()
	for _, rt := range tuns {
		rt.close()
	}
	for _, s := range sessions {
		s.close()
	}
}

// ServeControl upgrades an HTTP request into a tunnel control session.
func (h *Hub) ServeControl(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	ident, err := h.hooks.Authenticate(r.Context(), token)
	if err != nil {
		http.Error(w, `{"code":"UNAUTHENTICATED","message":"invalid token"}`, http.StatusUnauthorized)
		return
	}
	conn, err := h.upg.Upgrade(w, r, nil)
	if err != nil {
		h.log.Warn("control upgrade failed", "err", err)
		return
	}
	conn.SetReadLimit(1 << 20)
	sess, err := newClientSession(h, conn, ident)
	if err != nil {
		_ = conn.Close()
		return
	}
	h.mu.Lock()
	if old := h.sessions[ident.UserID]; old != nil {
		old.close()
	}
	h.sessions[ident.UserID] = sess
	h.mu.Unlock()
	h.log.Info("control session opened", "user", ident.Username)
	h.hooksLog("info", "客户端已连接", map[string]any{"user": ident.Username})
	go func() {
		sess.run()
		h.mu.Lock()
		if h.sessions[ident.UserID] == sess {
			delete(h.sessions, ident.UserID)
		}
		h.mu.Unlock()
		h.log.Info("control session closed", "user", ident.Username)
		h.hooksLog("info", "客户端已断开", map[string]any{"user": ident.Username})
	}()
}

// PushRegister asks a connected client session to start the given tunnels.
// It returns ErrClientOffline when no agent is connected for the user.
func (h *Hub) PushRegister(userID string, specs []TunnelSpec) error {
	h.mu.RLock()
	sess := h.sessions[userID]
	h.mu.RUnlock()
	if sess == nil {
		return ErrClientOffline
	}
	reqs := make([]RegisterTunnel, 0, len(specs))
	for _, s := range specs {
		reqs = append(reqs, RegisterTunnel{
			ID: s.ID, Proto: s.Proto, LocalIP: s.LocalIP, LocalPort: s.LocalPort,
			PublicPort: s.PublicPort, Domain: s.Domain, Bandwidth: s.BandwidthKbps, MaxConns: s.MaxConnections,
		})
	}
	return sess.sendRegister(reqs)
}

// StartTunnelFor starts a public listener for spec on the user's live session.
// It returns ErrClientOffline when no agent is connected.
func (h *Hub) StartTunnelFor(userID string, spec TunnelSpec) (string, error) {
	h.mu.RLock()
	sess := h.sessions[userID]
	h.mu.RUnlock()
	if sess == nil {
		return "", ErrClientOffline
	}
	rt, err := h.startTunnel(&spec, sess)
	if err != nil {
		return "", err
	}
	sess.mu.Lock()
	sess.tuns[spec.ID] = rt
	sess.mu.Unlock()
	return spec.Address(), nil
}

// StopTunnel closes the public listener immediately and tells the owning client
// session to forget the tunnel. It is idempotent.
func (h *Hub) StopTunnel(id string) bool {
	h.mu.RLock()
	rt := h.tunnels[id]
	h.mu.RUnlock()
	if rt == nil {
		return false
	}
	owner := rt.owner
	owner.stopTunnel(id)
	_ = WriteControl(owner.ctrl, &owner.writeM, ControlMessage{Type: MsgStop, IDs: []string{id}})
	return true
}

// PushStop asks a connected client session to stop the given tunnels.
func (h *Hub) PushStop(userID string, ids []string) error {
	h.mu.RLock()
	sess := h.sessions[userID]
	h.mu.RUnlock()
	if sess == nil {
		return ErrClientOffline
	}
	return WriteControl(sess.ctrl, &sess.writeM, ControlMessage{Type: MsgStop, IDs: ids})
}

// SessionOnline reports whether a client agent is connected.
func (h *Hub) SessionOnline(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.sessions[userID]
	return ok
}

// ActiveTunnels returns runtime addresses keyed by tunnel id.
func (h *Hub) ActiveTunnels() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]string, len(h.tunnels))
	for id, rt := range h.tunnels {
		out[id] = rt.spec.Address()
	}
	return out
}

// IsActive reports whether a tunnel currently has a public listener.
func (h *Hub) IsActive(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.tunnels[id]
	return ok
}

func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if len(v) > 7 && (v[:7] == "Bearer " || v[:7] == "bearer ") {
		return v[7:]
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return q
	}
	return ""
}

func (h *Hub) statsLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-t.C:
			h.mu.RLock()
			list := make([]*runtimeTunnel, 0, len(h.tunnels))
			for _, rt := range h.tunnels {
				list = append(list, rt)
			}
			h.mu.RUnlock()
			for _, rt := range list {
				rx := loadInt64(&rt.rx)
				tx := loadInt64(&rt.tx)
				conns := loadInt64(&rt.conns)
				active := int(loadInt32(&rt.active))
				if h.hooks.Stats != nil {
					h.hooks.Stats(rt.spec.ID, rx, tx, conns, active)
				}
				rt.pushStats(rx, tx, conns, active)
			}
		}
	}
}

func (h *Hub) hooksLog(level, msg string, kv map[string]any) {
	if h.hooks.Log != nil {
		h.hooks.Log(level, msg, kv)
	}
}

// ---- client session ----

type clientSession struct {
	hub    *Hub
	conn   *websocket.Conn
	ident  *Identity
	mux    *yamux.Session
	ctrl   net.Conn
	br     *bufio.Reader
	writeM sync.Mutex
	closed atomic.Bool
	done   chan struct{}
	tuns   map[string]*runtimeTunnel
	mu     sync.Mutex
}

func newClientSession(h *Hub, conn *websocket.Conn, ident *Identity) (*clientSession, error) {
	// The WebSocket is a byte stream for yamux. yamux only needs net.Conn.
	nc := wsnet.New(conn)
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = ioDiscard{}
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 20 * time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second
	mux, err := yamux.Server(nc, cfg)
	if err != nil {
		return nil, err
	}
	return &clientSession{
		hub: h, conn: conn, ident: ident, mux: mux,
		done: make(chan struct{}), tuns: map[string]*runtimeTunnel{},
	}, nil
}

func (s *clientSession) run() {
	defer s.close()
	// First stream is the control stream.
	stream, err := s.mux.AcceptStream()
	if err != nil {
		return
	}
	s.ctrl = stream
	s.br = bufio.NewReader(stream)
	hello, err := ReadControl(s.br)
	if err != nil || hello.Type != MsgHello {
		_ = WriteControl(s.ctrl, &s.writeM, ControlMessage{Type: MsgError, Code: "BAD_HELLO", Message: "expected hello"})
		return
	}
	_ = WriteControl(s.ctrl, &s.writeM, ControlMessage{
		Type: MsgHelloOK, Version: ProtocolVersion, Time: time.Now().UTC(),
		Message: s.ident.Username,
	})
	// Restore tunnels the user asked to keep running.
	if s.hub.hooks.OnSessionUp != nil {
		s.hub.hooks.OnSessionUp(s.hub.ctx, s.ident.UserID)
	}
	// Start accepting proxied streams.
	go s.acceptStreams()
	for {
		msg, err := ReadControl(s.br)
		if err != nil {
			return
		}
		switch msg.Type {
		case MsgRegister:
			s.handleRegister(msg.Tunnels)
		case MsgStop:
			for _, id := range msg.IDs {
				s.stopTunnel(id)
			}
		case MsgPing:
			_ = WriteControl(s.ctrl, &s.writeM, ControlMessage{Type: MsgPong, Time: time.Now().UTC()})
		case MsgStats:
			// client-reported counters are advisory; server counters win
		}
	}
}

func (s *clientSession) close() {
	if s.closed.Swap(true) {
		return
	}
	close(s.done)
	s.mu.Lock()
	for id, rt := range s.tuns {
		s.hub.removeTunnel(id, rt)
	}
	s.tuns = map[string]*runtimeTunnel{}
	s.mu.Unlock()
	if s.mux != nil {
		_ = s.mux.Close()
	}
	_ = s.conn.Close()
}

func (s *clientSession) handleRegister(reqs []RegisterTunnel) {
	results := make([]RegisterResult, 0, len(reqs))
	for _, req := range reqs {
		res := RegisterResult{ID: req.ID}
		spec, err := s.hub.hooks.LookupTunnel(s.hub.ctx, s.ident.UserID, req.ID)
		if err != nil {
			res.Error = "隧道不存在或无权访问"
			results = append(results, res)
			continue
		}
		if spec.PublicPort == 0 && req.PublicPort > 0 {
			spec.PublicPort = req.PublicPort
		}
		if spec.PublicPort == 0 {
			port, err := s.hub.hooks.AllocatePort(s.hub.ctx, spec)
			if err != nil {
				res.Error = err.Error()
				results = append(results, res)
				continue
			}
			spec.PublicPort = port
			if s.hub.hooks.SavePort != nil {
				_ = s.hub.hooks.SavePort(s.hub.ctx, spec.ID, port)
			}
		}
		rt, err := s.hub.startTunnel(spec, s)
		if err != nil {
			res.Error = humanListenError(err, *spec)
			results = append(results, res)
			continue
		}
		s.mu.Lock()
		if old := s.tuns[spec.ID]; old != nil && old != rt {
			old.close()
		}
		s.tuns[spec.ID] = rt
		s.mu.Unlock()
		res.OK = true
		res.Address = spec.Address()
		results = append(results, res)
	}
	_ = WriteControl(s.ctrl, &s.writeM, ControlMessage{Type: MsgRegistered, Results: results})
}

func humanListenError(err error, spec TunnelSpec) string {
	if errors.Is(err, errPortBusy) {
		return fmt.Sprintf("端口 %d 已被占用，请更换公网端口", spec.PublicPort)
	}
	return "启动失败：" + err.Error()
}

func (s *clientSession) stopTunnel(id string) {
	s.mu.Lock()
	rt := s.tuns[id]
	delete(s.tuns, id)
	s.mu.Unlock()
	if rt != nil {
		s.hub.removeTunnel(id, rt)
	}
}

// acceptStreams handles proxied connections opened by the client's peer.
func (s *clientSession) acceptStreams() {
	for {
		stream, err := s.mux.AcceptStream()
		if err != nil {
			return
		}
		go s.handleStream(stream)
	}
}

func (s *clientSession) handleStream(stream net.Conn) {
	br := bufio.NewReader(stream)
	hdr, err := ReadHeader(br)
	if err != nil {
		_ = stream.Close()
		return
	}
	s.mu.Lock()
	rt := s.tuns[hdr.TunnelID]
	s.mu.Unlock()
	if rt == nil {
		_ = WriteControl(s.ctrl, &s.writeM, ControlMessage{Type: MsgError, Code: "UNKNOWN_TUNNEL", Message: hdr.TunnelID})
		_ = stream.Close()
		return
	}
	// For HTTP the server writes the request onto the stream and the client
	// simply dials the local service. The server-side reverse proxy owns the
	// request/response framing, so here we only bridge bytes.
	rt.bridge(stream)
}

// sendRegister is used by admin APIs to ask a live session to (re)register.
func (s *clientSession) sendRegister(specs []RegisterTunnel) error {
	return WriteControl(s.ctrl, &s.writeM, ControlMessage{Type: MsgRegister, Tunnels: specs})
}

// ---- runtime tunnel ----

var errPortBusy = errors.New("port busy")

// ErrClientOffline means no client agent is connected for the user.
var ErrClientOffline = errors.New("client offline")

type runtimeTunnel struct {
	spec   TunnelSpec
	owner  *clientSession
	hub    *Hub
	ln     net.Listener
	udp    *net.UDPConn
	http   *http.Server
	closeO sync.Once
	done   chan struct{}

	rx, tx, conns int64
	active        int32

	udpMu  sync.Mutex
	udpSes map[string]*udpSession
}

type udpSession struct {
	conn   net.Conn
	addr   *net.UDPAddr
	last   atomic.Int64
	closed atomic.Bool
}

func (h *Hub) startTunnel(spec *TunnelSpec, s *clientSession) (*runtimeTunnel, error) {
	h.mu.Lock()
	if old, ok := h.tunnels[spec.ID]; ok {
		old.close()
		delete(h.tunnels, spec.ID)
	}
	if spec.PublicPort > 0 {
		if owner, taken := h.ports[spec.PublicPort]; taken && owner != spec.ID {
			h.mu.Unlock()
			return nil, errPortBusy
		}
	}
	h.mu.Unlock()

	// "https" is a raw TLS passthrough: the user's local service terminates
	// TLS with its own certificate, so the tunnel stays end-to-end encrypted.
	rt := &runtimeTunnel{spec: *spec, owner: s, hub: h, done: make(chan struct{}), udpSes: map[string]*udpSession{}}
	addr := net.JoinHostPort(spec.BindAddr, strconv.Itoa(spec.PublicPort))
	if spec.BindAddr == "" {
		addr = ":" + strconv.Itoa(spec.PublicPort)
	}
	var err error
	switch spec.Proto {
	case "tcp", "https":
		rt.ln, err = net.Listen("tcp", addr)
		if err != nil {
			return nil, wrapBusy(err)
		}
		go rt.acceptTCP()
	case "http":
		ln, lerr := net.Listen("tcp", addr)
		if lerr != nil {
			return nil, wrapBusy(lerr)
		}
		rt.ln = ln
		rt.http = &http.Server{
			Handler:           rt.proxyHandler(),
			ReadHeaderTimeout: 20 * time.Second,
			IdleTimeout:       90 * time.Second,
		}
		go func() { _ = rt.http.Serve(ln) }()
	case "udp":
		uaddr, rerr := net.ResolveUDPAddr("udp", addr)
		if rerr != nil {
			return nil, rerr
		}
		rt.udp, err = net.ListenUDP("udp", uaddr)
		if err != nil {
			return nil, wrapBusy(err)
		}
		go rt.serveUDP()
	default:
		return nil, fmt.Errorf("unsupported protocol %q", spec.Proto)
	}

	h.mu.Lock()
	h.tunnels[spec.ID] = rt
	if spec.PublicPort > 0 {
		h.ports[spec.PublicPort] = spec.ID
	}
	h.mu.Unlock()
	h.log.Info("tunnel listening", "tunnel", spec.ID, "proto", spec.Proto, "addr", spec.Address())
	h.hooksLog("info", "隧道已启动", map[string]any{"tunnel": spec.ID, "address": spec.Address()})
	return rt, nil
}

func wrapBusy(err error) error {
	if errors.Is(err, errPortBusy) {
		return err
	}
	return err
}

func (h *Hub) removeTunnel(id string, rt *runtimeTunnel) {
	h.mu.Lock()
	if cur, ok := h.tunnels[id]; ok && cur == rt {
		delete(h.tunnels, id)
	}
	if owner, ok := h.ports[rt.spec.PublicPort]; ok && owner == id {
		delete(h.ports, rt.spec.PublicPort)
	}
	h.mu.Unlock()
	rt.close()
}

func (rt *runtimeTunnel) close() {
	rt.closeO.Do(func() {
		close(rt.done)
		if rt.http != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = rt.http.Shutdown(ctx)
			cancel()
		}
		if rt.ln != nil {
			_ = rt.ln.Close()
		}
		if rt.udp != nil {
			_ = rt.udp.Close()
		}
		rt.udpMu.Lock()
		for _, us := range rt.udpSes {
			us.closed.Store(true)
			_ = us.conn.Close()
		}
		rt.udpSes = map[string]*udpSession{}
		rt.udpMu.Unlock()
		rt.hub.log.Info("tunnel stopped", "tunnel", rt.spec.ID)
		rt.hub.hooksLog("info", "隧道已停止", map[string]any{"tunnel": rt.spec.ID})
	})
}

func (rt *runtimeTunnel) overLimit() bool {
	if rt.spec.MaxConnections <= 0 {
		return false
	}
	return int(loadInt32(&rt.active)) >= rt.spec.MaxConnections
}

func (rt *runtimeTunnel) openStream(remote string) (net.Conn, error) {
	st, err := rt.owner.mux.OpenStream()
	if err != nil {
		return nil, err
	}
	if err := WriteHeader(st, StreamHeader{
		TunnelID: rt.spec.ID, RemoteAddr: remote, Kind: rt.spec.Proto,
		LocalIP: rt.spec.LocalIP, LocalPort: rt.spec.LocalPort, Proto: rt.spec.Proto,
	}); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

func (rt *runtimeTunnel) acceptTCP() {
	for {
		c, err := rt.ln.Accept()
		if err != nil {
			return
		}
		if rt.overLimit() {
			_ = c.Close()
			continue
		}
		addInt32(&rt.active, 1)
		addInt64(&rt.conns, 1)
		go rt.bridge(c)
	}
}

// bridge copies between a public connection and a fresh yamux stream.
func (rt *runtimeTunnel) bridge(public net.Conn) {
	defer func() {
		addInt32(&rt.active, -1)
		_ = public.Close()
	}()
	st, err := rt.openStream(public.RemoteAddr().String())
	if err != nil {
		return
	}
	defer st.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		n, _ := limitCopy(st, public, rt.spec.BandwidthKbps, &rt.rx)
		_ = n
		if cw, ok := st.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = limitCopy(public, st, rt.spec.BandwidthKbps, &rt.tx)
		if cw, ok := public.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	wg.Wait()
}

func (rt *runtimeTunnel) proxyHandler() http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// The upstream address is virtual: the client dials its own
			// local_ip:local_port. Keep the original Host for applications
			// that depend on it and expose it via X-Forwarded-Host.
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = net.JoinHostPort(rt.spec.LocalIP, strconv.Itoa(rt.spec.LocalPort))
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-YukiTunnel", rt.spec.ID)
		},
		Transport:      &streamTransport{rt: rt},
		ErrorLog:       nil,
		FlushInterval:  -1,
		ModifyResponse: func(resp *http.Response) error { return nil },
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt.overLimit() {
			http.Error(w, "too many connections", http.StatusServiceUnavailable)
			return
		}
		// Each tunnel owns a dedicated public port, so Host-based routing is
		// not required. The original Host is preserved for the local app.
		addInt32(&rt.active, 1)
		addInt64(&rt.conns, 1)
		defer addInt32(&rt.active, -1)
		proxy.ServeHTTP(w, r)
	})
}

// streamTransport turns each HTTP request into one yamux stream.
type streamTransport struct{ rt *runtimeTunnel }

func (t *streamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	st, err := t.rt.openStream(req.RemoteAddr)
	if err != nil {
		return nil, err
	}
	if err := req.Write(st); err != nil {
		_ = st.Close()
		return nil, err
	}
	br := bufio.NewReader(st)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	resp.Body = &streamBody{body: resp.Body, conn: st}
	addInt64(&t.rt.tx, 1)
	return resp, nil
}

type streamBody struct {
	body io.ReadCloser
	conn net.Conn
}

func (b *streamBody) Read(p []byte) (int, error) { return b.body.Read(p) }

func (b *streamBody) Close() error {
	err := b.body.Close()
	_ = b.conn.Close()
	return err
}

func (rt *runtimeTunnel) serveUDP() {
	buf := make([]byte, 65535)
	for {
		n, addr, err := rt.udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if rt.overLimit() {
			continue
		}
		key := addr.String()
		rt.udpMu.Lock()
		us := rt.udpSes[key]
		if us == nil {
			st, err := rt.openStream(key)
			if err != nil {
				rt.udpMu.Unlock()
				continue
			}
			us = &udpSession{conn: st, addr: addr}
			us.last.Store(time.Now().Unix())
			rt.udpSes[key] = us
			addInt64(&rt.conns, 1)
			addInt32(&rt.active, 1)
			go rt.udpReadLoop(us)
		}
		rt.udpMu.Unlock()
		us.last.Store(time.Now().Unix())
		if err := WriteDatagram(us.conn, buf[:n]); err != nil {
			rt.dropUDP(key, us)
			continue
		}
		addInt64(&rt.rx, int64(n))
	}
}

func (rt *runtimeTunnel) udpReadLoop(us *udpSession) {
	defer func() {
		addInt32(&rt.active, -1)
		rt.dropUDP(us.addr.String(), us)
	}()
	for {
		pkt, err := ReadDatagram(us.conn)
		if err != nil {
			return
		}
		if _, err := rt.udp.WriteToUDP(pkt, us.addr); err != nil {
			return
		}
		addInt64(&rt.tx, int64(len(pkt)))
	}
}

func (rt *runtimeTunnel) dropUDP(key string, us *udpSession) {
	rt.udpMu.Lock()
	if cur := rt.udpSes[key]; cur == us {
		delete(rt.udpSes, key)
	}
	rt.udpMu.Unlock()
	if us.closed.CompareAndSwap(false, true) {
		_ = us.conn.Close()
	}
}

func (rt *runtimeTunnel) pushStats(rx, tx, conns int64, active int) {
	_ = WriteControl(rt.owner.ctrl, &rt.owner.writeM, ControlMessage{
		Type: MsgStats,
		Stats: map[string]TunnelStats{rt.spec.ID: {
			RxBytes: rx, TxBytes: tx, Conns: conns, Active: active,
		}},
	})
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
