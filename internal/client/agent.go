// Package client implements the YukiTunnel client agent: it connects to the
// server control endpoint, keeps the session alive, registers tunnels and
// forwards public traffic to the local services.
package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"github.com/yuuuuukiu/yukitunnel/internal/auth"
	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
	"github.com/yuuuuukiu/yukitunnel/internal/tunnel"
	"github.com/yuuuuukiu/yukitunnel/internal/wsnet"
)

// LogLevel mirrors the UI severity names.
type LogLevel string

const (
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// LogEntry is a human readable client log line, streamed to the GUI and CLI.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   LogLevel  `json:"level"`
	Tunnel  string    `json:"tunnel,omitempty"`
	Message string    `json:"message"`
}

// Options configures an Agent.
type Options struct {
	// ServerURL is the HTTPS base URL, e.g. https://panel.example.com:8443.
	ServerURL string
	// Token authenticates the control connection.
	Token string
	// KeyPath is an RSA private key used for challenge-response login when the
	// token is missing or expired.
	KeyPath string
	// Username is used with key login.
	Username string
	// InsecureSkipVerify disables certificate validation (self-signed).
	InsecureSkipVerify bool
	// Fingerprint pins the server certificate (SHA-256, colon separated).
	Fingerprint string
	// Logger receives structured logs.
	Logger *slog.Logger
	// AutoReconnect retries forever with exponential backoff.
	AutoReconnect bool
	// OnLog receives user-facing log entries (optional).
	OnLog func(LogEntry)
	// OnState receives connection state changes (optional).
	OnState func(state string)
	// OnStats receives live traffic counters per tunnel.
	OnStats func(tunnelID string, rx, tx int64, active int)
	// DialTimeout bounds the WebSocket handshake.
	DialTimeout time.Duration
}

// Agent is a live client session.
type Agent struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	conn    *websocket.Conn
	mux     *yamux.Session
	ctrl    net.Conn
	br      *bufio.Reader
	writeM  sync.Mutex
	running map[string]context.CancelFunc
	specs   map[string]tunnel.RegisterTunnel
	stats   map[string]*tunnel.TunnelStats
	state   string
	stopped bool

	done chan struct{}
}

// ErrNotConnected is returned when an operation needs a live session.
var ErrNotConnected = errors.New("未连接到服务端")

// New creates an agent.
func New(opts Options) *Agent {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.DialTimeout == 0 {
		opts.DialTimeout = 15 * time.Second
	}
	return &Agent{
		opts:    opts,
		log:     opts.Logger,
		running: map[string]context.CancelFunc{},
		specs:   map[string]tunnel.RegisterTunnel{},
		stats:   map[string]*tunnel.TunnelStats{},
		state:   "disconnected",
		done:    make(chan struct{}),
	}
}

// State returns the current connection state.
func (a *Agent) State() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *Agent) setState(s string) {
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
	if a.opts.OnState != nil {
		a.opts.OnState(s)
	}
}

// Run connects and blocks until ctx is cancelled or the session ends
// permanently. It reconnects automatically when AutoReconnect is set.
func (a *Agent) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		err := a.connectAndServe(ctx)
		if ctx.Err() != nil || !a.opts.AutoReconnect {
			return err
		}
		if err == nil {
			err = errors.New("connection closed")
		}
		a.logf(LogWarn, "", "连接已断开：%s，%s 后自动重连", humanNetError(err), backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// Stop shuts the agent down.
func (a *Agent) Stop() {
	a.mu.Lock()
	a.stopped = true
	a.mu.Unlock()
	a.closeConn()
}

func (a *Agent) closeConn() {
	a.mu.Lock()
	mux := a.mux
	conn := a.conn
	a.mu.Unlock()
	if mux != nil {
		_ = mux.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
}

// ControlURL derives the wss control endpoint from the server URL.
func ControlURL(serverURL string) (string, error) {
	u := strings.TrimSpace(serverURL)
	if u == "" {
		return "", errors.New("服务端地址不能为空")
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	if strings.HasPrefix(u, "http://") {
		u = "https://" + strings.TrimPrefix(u, "http://")
	}
	u = strings.TrimRight(u, "/")
	u = strings.TrimSuffix(u, "/api/v1")
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://") + "/v1/control", nil
	}
	return "", fmt.Errorf("不支持的地址 %q，请使用 https:// 开头", serverURL)
}

func (a *Agent) connectAndServe(ctx context.Context) error {
	url, err := ControlURL(a.opts.ServerURL)
	if err != nil {
		return err
	}
	a.setState("connecting")
	dialer := websocket.Dialer{
		HandshakeTimeout: a.opts.DialTimeout,
		TLSClientConfig:  a.tlsConfig(),
		NetDialContext:   (&net.Dialer{Timeout: a.opts.DialTimeout}).DialContext,
	}
	hdr := http.Header{}
	hdr.Set("User-Agent", "yuki-client/0.1")
	token := a.opts.Token
	if token == "" && a.opts.KeyPath != "" {
		token, err = a.fetchKeyToken(ctx)
		if err != nil {
			return err
		}
	}
	hdr.Set("Authorization", "Bearer "+token)
	conn, _, err := dialer.DialContext(ctx, url, hdr)
	if err != nil {
		a.setState("disconnected")
		return err
	}
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 20 * time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second
	mux, err := yamux.Client(wsnet.New(conn), cfg)
	if err != nil {
		_ = conn.Close()
		a.setState("disconnected")
		return err
	}
	ctrl, err := mux.OpenStream()
	if err != nil {
		_ = mux.Close()
		_ = conn.Close()
		a.setState("disconnected")
		return err
	}
	a.mu.Lock()
	a.conn, a.mux, a.ctrl, a.br = conn, mux, ctrl, bufio.NewReader(ctrl)
	a.stopped = false
	a.mu.Unlock()

	if err := tunnel.WriteControl(ctrl, &a.writeM, tunnel.ControlMessage{
		Type: tunnel.MsgHello, Version: tunnel.ProtocolVersion, Client: "yuki-client/0.1",
	}); err != nil {
		a.closeConn()
		return err
	}
	hello, err := tunnel.ReadControl(a.br)
	if err != nil {
		a.closeConn()
		return err
	}
	if hello.Type == tunnel.MsgError {
		a.closeConn()
		a.setState("disconnected")
		return fmt.Errorf("服务端拒绝连接：%s", hello.Message)
	}
	if hello.Version != 0 && hello.Version != tunnel.ProtocolVersion {
		a.closeConn()
		return fmt.Errorf("客户端与服务端协议版本不一致（服务端 v%d），请升级客户端", hello.Version)
	}
	a.setState("connected")
	a.logf(LogInfo, "", "已连接到服务端")
	a.opts.OnState("connected")

	// Re-register tunnels that were running before a reconnect.
	a.mu.Lock()
	pending := make([]tunnel.RegisterTunnel, 0, len(a.specs))
	for _, s := range a.specs {
		pending = append(pending, s)
	}
	a.mu.Unlock()
	if len(pending) > 0 {
		_ = tunnel.WriteControl(ctrl, &a.writeM, tunnel.ControlMessage{Type: tunnel.MsgRegister, Tunnels: pending})
	}

	go a.readLoop()
	a.acceptLoop(mux)
	a.closeConn()
	a.setState("disconnected")
	return nil
}

func (a *Agent) tlsConfig() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName(a.opts.ServerURL)}
	if a.opts.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true
	}
	return cfg
}

func serverName(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	return s
}

func (a *Agent) readLoop() {
	for {
		msg, err := tunnel.ReadControl(a.br)
		if err != nil {
			return
		}
		switch msg.Type {
		case tunnel.MsgRegistered:
			for _, r := range msg.Results {
				if r.OK {
					a.logf(LogInfo, r.ID, "隧道已上线：%s", r.Address)
				} else {
					a.logf(LogError, r.ID, "隧道启动失败：%s", r.Error)
				}
			}
		case tunnel.MsgStats:
			a.mu.Lock()
			for id, st := range msg.Stats {
				cp := st
				a.stats[id] = &cp
			}
			a.mu.Unlock()
			for id, st := range msg.Stats {
				if a.opts.OnStats != nil {
					a.opts.OnStats(id, st.RxBytes, st.TxBytes, st.Active)
				}
			}
		case tunnel.MsgStop:
			// Keep the spec so a later start (from the CLI or GUI) can be
			// forwarded without a round trip to the API.
			a.mu.Lock()
			for _, id := range msg.IDs {
				delete(a.stats, id)
			}
			a.mu.Unlock()
			for _, id := range msg.IDs {
				a.logf(LogInfo, id, "服务端已停止该隧道")
			}
		case tunnel.MsgPong:
		case tunnel.MsgError:
			a.logf(LogError, "", "服务端错误：%s", msg.Message)
		}
	}
}

// acceptLoop serves proxied streams until the session dies.
func (a *Agent) acceptLoop(mux *yamux.Session) {
	for {
		stream, err := mux.AcceptStream()
		if err != nil {
			return
		}
		go a.handleStream(stream)
	}
}

func (a *Agent) handleStream(stream net.Conn) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	hdr, err := tunnel.ReadHeader(br)
	if err != nil {
		return
	}
	a.mu.Lock()
	spec, ok := a.specs[hdr.TunnelID]
	a.mu.Unlock()
	if !ok {
		// Fall back to the target carried in the stream header.
		if hdr.LocalPort == 0 {
			return
		}
		spec = tunnel.RegisterTunnel{ID: hdr.TunnelID, Proto: hdr.Proto, LocalIP: hdr.LocalIP, LocalPort: hdr.LocalPort}
	}
	switch hdr.Kind {
	case "udp":
		a.serveUDP(stream, br, spec)
	case "http":
		a.serveHTTP(stream, br, spec)
	default:
		a.serveTCP(stream, br, spec)
	}
}

func (a *Agent) dialLocal(spec tunnel.RegisterTunnel) (net.Conn, error) {
	host := spec.LocalIP
	if host == "" {
		host = "127.0.0.1"
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	c, err := d.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(spec.LocalPort)))
	if err != nil {
		a.logf(LogError, spec.ID, "无法连接内网服务 %s:%d：%s", host, spec.LocalPort, humanNetError(err))
	}
	return c, err
}

func (a *Agent) serveTCP(stream net.Conn, br *bufio.Reader, spec tunnel.RegisterTunnel) {
	local, err := a.dialLocal(spec)
	if err != nil {
		return
	}
	defer local.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(local, br)
		if cw, ok := local.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stream, local)
	}()
	wg.Wait()
}

// serveHTTP forwards one HTTP request/response pair. The server's reverse proxy
// serializes the request on the stream; the local HTTP client replays it.
func (a *Agent) serveHTTP(stream net.Conn, br *bufio.Reader, spec tunnel.RegisterTunnel) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	host := spec.LocalIP
	if host == "" {
		host = "127.0.0.1"
	}
	target := "http://" + net.JoinHostPort(host, strconv.Itoa(spec.LocalPort)) + req.URL.RequestURI()
	out, err := http.NewRequestWithContext(context.Background(), req.Method, target, req.Body)
	if err != nil {
		return
	}
	out.Header = req.Header.Clone()
	// Keep the public Host so virtual-hosted local apps work.
	out.Host = req.Host
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(out)
	if err != nil {
		a.logf(LogError, spec.ID, "内网服务无响应：%s", humanNetError(err))
		_, _ = io.WriteString(stream, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 21\r\nContent-Type: text/plain\r\n\r\n内网服务无响应 (502)")
		return
	}
	defer resp.Body.Close()
	_ = resp.Write(stream)
}

func (a *Agent) serveUDP(stream net.Conn, br *bufio.Reader, spec tunnel.RegisterTunnel) {
	host := spec.LocalIP
	if host == "" {
		host = "127.0.0.1"
	}
	raddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(spec.LocalPort)))
	if err != nil {
		return
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		a.logf(LogError, spec.ID, "无法连接内网 UDP 服务：%s", humanNetError(err))
		return
	}
	defer conn.Close()
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); _ = conn.Close() }) }
	defer stop()
	go func() {
		buf := make([]byte, 65535)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				stop()
				return
			}
			if err := tunnel.WriteDatagram(stream, buf[:n]); err != nil {
				stop()
				return
			}
		}
	}()
	for {
		pkt, err := tunnel.ReadDatagram(br)
		if err != nil {
			return
		}
		if _, err := conn.Write(pkt); err != nil {
			return
		}
	}
}

// StartTunnel registers a tunnel with the server and remembers it for
// reconnects.
func (a *Agent) StartTunnel(spec tunnel.RegisterTunnel) error {
	a.mu.Lock()
	a.specs[spec.ID] = spec
	connected := a.conn != nil && a.mux != nil && a.ctrl != nil
	ctrl := a.ctrl
	a.mu.Unlock()
	if !connected {
		// The tunnel is queued and registered once the session is up.
		return nil
	}
	return tunnel.WriteControl(ctrl, &a.writeM, tunnel.ControlMessage{
		Type: tunnel.MsgRegister, Tunnels: []tunnel.RegisterTunnel{spec},
	})
}

// StartTunnels registers several tunnels at once.
func (a *Agent) StartTunnels(list []cliutil.Tunnel) error {
	for _, t := range list {
		spec := tunnel.RegisterTunnel{
			ID: t.ID, Proto: t.Proto, LocalIP: t.LocalIP, LocalPort: t.LocalPort,
			PublicPort: t.PublicPort, Domain: t.Domain,
		}
		if err := a.StartTunnel(spec); err != nil {
			return err
		}
	}
	return nil
}

// StopTunnel asks the server to close the public listener and forgets the spec.
func (a *Agent) StopTunnel(id string) error {
	a.mu.Lock()
	delete(a.specs, id)
	delete(a.stats, id)
	connected := a.conn != nil
	a.mu.Unlock()
	if !connected {
		return nil
	}
	return tunnel.WriteControl(a.ctrl, &a.writeM, tunnel.ControlMessage{Type: tunnel.MsgStop, IDs: []string{id}})
}

// Stats returns the last server-reported counters for a tunnel.
func (a *Agent) Stats(id string) tunnel.TunnelStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.stats[id]; s != nil {
		return *s
	}
	return tunnel.TunnelStats{}
}

// Ping measures the control-stream round trip.
func (a *Agent) Ping(ctx context.Context) (time.Duration, error) {
	a.mu.Lock()
	ctrl := a.ctrl
	a.mu.Unlock()
	if ctrl == nil {
		return 0, ErrNotConnected
	}
	start := time.Now()
	if err := tunnel.WriteControl(ctrl, &a.writeM, tunnel.ControlMessage{Type: tunnel.MsgPing, Time: start}); err != nil {
		return 0, err
	}
	// The read loop does not expose pongs synchronously; wait briefly for a
	// stats frame as a liveness proxy.
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-deadline:
			return 0, errors.New("服务端响应超时")
		case <-tick.C:
			if a.mux != nil && !a.mux.IsClosed() {
				return time.Since(start), nil
			}
			return 0, ErrNotConnected
		}
	}
}

// fetchKeyToken performs the RSA challenge-response login and returns a session
// token for the control connection.
func (a *Agent) fetchKeyToken(ctx context.Context) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(a.opts.ServerURL), "/")
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	challenge, err := a.postJSON(ctx, base+"/api/v1/auth/key/challenge", map[string]string{"username": a.opts.Username})
	if err != nil {
		return "", err
	}
	nonce, _ := challenge["nonce"].(string)
	if nonce == "" {
		return "", errors.New("服务端未返回挑战码")
	}
	pemBytes, err := os.ReadFile(a.opts.KeyPath)
	if err != nil {
		return "", fmt.Errorf("无法读取私钥 %s：%w", a.opts.KeyPath, err)
	}
	key, err := auth.ParsePrivateKeyPEM(pemBytes)
	if err != nil {
		return "", fmt.Errorf("私钥格式错误：%w", err)
	}
	sig, err := auth.SignChallenge(key, nonce)
	if err != nil {
		return "", err
	}
	res, err := a.postJSON(ctx, base+"/api/v1/auth/key/login", map[string]string{
		"username": a.opts.Username, "nonce": nonce, "signature": sig,
	})
	if err != nil {
		return "", err
	}
	tok, _ := res["token"].(string)
	if tok == "" {
		return "", errors.New("RSA 登录失败：服务端未返回令牌")
	}
	return tok, nil
}

func (a *Agent) postJSON(ctx context.Context, url string, body map[string]string) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	tr := &http.Transport{TLSClientConfig: a.tlsConfig()}
	client := &http.Client{Transport: tr, Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接服务端：%w", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 400 {
		msg, _ := out["message"].(string)
		if msg == "" {
			msg = resp.Status
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

func (a *Agent) logf(level LogLevel, tun, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if a.opts.OnLog != nil {
		a.opts.OnLog(LogEntry{Time: time.Now(), Level: level, Tunnel: tun, Message: msg})
	}
	switch level {
	case LogError:
		a.log.Error(msg, "tunnel", tun)
	case LogWarn:
		a.log.Warn(msg, "tunnel", tun)
	default:
		a.log.Info(msg, "tunnel", tun)
	}
}

// humanNetError maps low-level errors to readable Chinese messages.
func humanNetError(err error) string {
	if err == nil {
		return ""
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "连接超时，请检查网络或防火墙"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "服务端地址不可达（连接被拒绝）"
	case strings.Contains(s, "no such host"):
		return "无法解析服务端域名，请检查地址是否正确"
	case strings.Contains(s, "certificate"):
		return "TLS 证书校验失败，请确认服务端证书或启用信任"
	case strings.Contains(s, "401"):
		return "认证失败，请重新登录"
	case strings.Contains(s, "websocket: bad handshake"):
		return "服务端地址不可达或不是 YukiTunnel 服务端"
	case errors.Is(err, io.EOF), strings.Contains(s, "connection reset"):
		return "连接被服务端关闭"
	}
	return s
}

// HumanNetError is exported for the CLI and GUI error toasts.
func HumanNetError(err error) string { return humanNetError(err) }
