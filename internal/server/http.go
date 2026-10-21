package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/apierr"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
)

// httpServer owns the public HTTPS listener (REST API + WebUI + control WS).
type httpServer struct {
	svc    *Service
	srv    *http.Server
	ln     net.Listener
	ctlSrv *http.Server
	ctlLn  net.Listener
	once   sync.Once
}

func newHTTPServer(s *Service) *httpServer {
	h := &httpServer{svc: s}
	mux := http.NewServeMux()
	h.routes(mux)
	h.srv = &http.Server{
		Handler:           h.withMiddleware(mux),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	// The control listener carries only the agent WebSocket. Keeping it on a
	// separate port lets operators expose it publicly without exposing the UI.
	ctlMux := http.NewServeMux()
	ctlMux.HandleFunc("/v1/control", h.svc.Hub.ServeControl)
	ctlMux.HandleFunc("/health", h.handleHealth)
	h.ctlSrv = &http.Server{
		Handler:           ctlMux,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       0, // long-lived WebSockets
	}
	return h
}

func (h *httpServer) start() error {
	cert, key, err := h.svc.EnsureTLS()
	if err != nil {
		return fmt.Errorf("TLS: %w", err)
	}
	ln, err := net.Listen("tcp", h.svc.Cfg.APIAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", h.svc.Cfg.APIAddr, err)
	}
	h.ln = ln
	h.svc.Log.Info("API listening", "addr", h.svc.Cfg.APIAddr, "tls", true)
	go func() {
		if err := h.srv.ServeTLS(ln, cert, key); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.svc.Log.Error("API server stopped", "err", err)
		}
	}()
	if h.svc.Cfg.ControlAddr != "" && h.svc.Cfg.ControlAddr != h.svc.Cfg.APIAddr {
		cln, err := net.Listen("tcp", h.svc.Cfg.ControlAddr)
		if err != nil {
			h.svc.Log.Warn("control listener unavailable", "addr", h.svc.Cfg.ControlAddr, "err", err)
		} else {
			h.ctlLn = cln
			h.svc.Log.Info("control listening", "addr", h.svc.Cfg.ControlAddr, "tls", true)
			go func() {
				if err := h.ctlSrv.ServeTLS(cln, cert, key); err != nil && !errors.Is(err, http.ErrServerClosed) {
					h.svc.Log.Error("control server stopped", "err", err)
				}
			}()
		}
	}
	return nil
}

func (h *httpServer) shutdown(ctx context.Context) error {
	var first error
	if h.srv != nil {
		if err := h.srv.Shutdown(ctx); err != nil {
			first = err
		}
	}
	if h.ctlSrv != nil {
		if err := h.ctlSrv.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// routes registers every endpoint. /api/v1 is versioned; /v1/control is the
// data-plane WebSocket.
func (h *httpServer) routes(mux *http.ServeMux) {
	api := http.NewServeMux()

	// public
	api.HandleFunc("GET /health", h.handleHealth)
	api.HandleFunc("GET /meta", h.handleMeta)
	api.HandleFunc("POST /auth/login", h.handleLogin)
	api.HandleFunc("POST /auth/key/challenge", h.handleKeyChallenge)
	api.HandleFunc("POST /auth/key/login", h.handleKeyLogin)

	// authenticated
	api.HandleFunc("POST /auth/logout", h.auth(h.handleLogout, false))
	api.HandleFunc("GET /me", h.auth(h.handleMe, false))
	api.HandleFunc("POST /me/password", h.auth(h.handleChangePassword, false))
	api.HandleFunc("GET /me/keys", h.auth(h.handleListMyKeys, false))
	api.HandleFunc("POST /me/keys", h.auth(h.handleAddMyKey, false))
	api.HandleFunc("DELETE /me/keys/{id}", h.auth(h.handleDeleteMyKey, false))
	api.HandleFunc("GET /me/tokens", h.auth(h.handleListMyTokens, false))
	api.HandleFunc("POST /me/tokens", h.auth(h.handleCreateMyToken, false))
	api.HandleFunc("DELETE /me/tokens/{id}", h.auth(h.handleRevokeMyToken, false))

	// client-facing
	api.HandleFunc("GET /servers", h.auth(h.handleListServers, false))
	api.HandleFunc("GET /tunnels", h.auth(h.handleListMyTunnels, false))
	api.HandleFunc("POST /tunnels", h.auth(h.handleCreateTunnel, false))
	api.HandleFunc("GET /tunnels/{id}", h.auth(h.handleGetTunnel, false))
	api.HandleFunc("PATCH /tunnels/{id}", h.auth(h.handleUpdateTunnel, false))
	api.HandleFunc("DELETE /tunnels/{id}", h.auth(h.handleDeleteTunnel, false))
	api.HandleFunc("POST /tunnels/{id}/start", h.auth(h.handleStartTunnel, false))
	api.HandleFunc("POST /tunnels/{id}/stop", h.auth(h.handleStopTunnel, false))
	api.HandleFunc("GET /tunnels/{id}/stats", h.auth(h.handleTunnelStats, false))

	// admin
	api.HandleFunc("GET /admin/overview", h.auth(h.handleOverview, true))
	api.HandleFunc("GET /admin/users", h.auth(h.handleAdminListUsers, true))
	api.HandleFunc("POST /admin/users", h.auth(h.handleAdminCreateUser, true))
	api.HandleFunc("PATCH /admin/users/{id}", h.auth(h.handleAdminUpdateUser, true))
	api.HandleFunc("DELETE /admin/users/{id}", h.auth(h.handleAdminDeleteUser, true))
	api.HandleFunc("GET /admin/users/{id}/keys", h.auth(h.handleAdminListKeys, true))
	api.HandleFunc("POST /admin/users/{id}/keys", h.auth(h.handleAdminAddKey, true))
	api.HandleFunc("DELETE /admin/users/{id}/keys/{keyID}", h.auth(h.handleAdminDeleteKey, true))

	api.HandleFunc("GET /admin/nodes", h.auth(h.handleAdminListNodes, true))
	api.HandleFunc("POST /admin/nodes", h.auth(h.handleAdminCreateNode, true))
	api.HandleFunc("PATCH /admin/nodes/{id}", h.auth(h.handleAdminUpdateNode, true))
	api.HandleFunc("DELETE /admin/nodes/{id}", h.auth(h.handleAdminDeleteNode, true))
	api.HandleFunc("POST /admin/nodes/{id}/check", h.auth(h.handleAdminCheckNode, true))

	api.HandleFunc("GET /admin/tunnels", h.auth(h.handleAdminListTunnels, true))
	api.HandleFunc("DELETE /admin/tunnels/{id}", h.auth(h.handleAdminDeleteTunnel, true))
	api.HandleFunc("POST /admin/tunnels/{id}/disable", h.auth(h.handleAdminDisableTunnel, true))
	api.HandleFunc("POST /admin/tunnels/{id}/enable", h.auth(h.handleAdminEnableTunnel, true))

	api.HandleFunc("GET /admin/settings", h.auth(h.handleGetSettings, true))
	api.HandleFunc("PUT /admin/settings", h.auth(h.handleUpdateSettings, true))
	api.HandleFunc("GET /admin/audit", h.auth(h.handleAudit, true))
	api.HandleFunc("GET /admin/ports", h.auth(h.handlePortUsage, true))

	// cloud profiles (registry, install, credentials, execution)
	api.HandleFunc("GET /admin/profiles/index", h.auth(h.handleProfileIndex, true))
	api.HandleFunc("GET /admin/profiles", h.auth(h.handleProfileList, true))
	api.HandleFunc("POST /admin/profiles/install", h.auth(h.handleProfileInstall, true))
	api.HandleFunc("GET /admin/profiles/{id}", h.auth(h.handleProfileGet, true))
	api.HandleFunc("DELETE /admin/profiles/{id}", h.auth(h.handleProfileDelete, true))
	api.HandleFunc("POST /admin/profiles/{id}/{state}", h.auth(h.handleProfileToggle, true))
	api.HandleFunc("GET /admin/profiles/{id}/credentials", h.auth(h.handleProfileCredentialList, true))
	api.HandleFunc("PUT /admin/profiles/{id}/credentials", h.auth(h.handleProfileCredentialPut, true))
	api.HandleFunc("DELETE /admin/profiles/{id}/credentials/{credID}", h.auth(h.handleProfileCredentialDelete, true))
	api.HandleFunc("POST /admin/profiles/{id}/run", h.auth(h.handleProfileRun, true))
	api.HandleFunc("GET /admin/profile-runs", h.auth(h.handleProfileRuns, true))
	api.HandleFunc("GET /admin/profile-settings", h.auth(h.handleProfileSettings, true))
	api.HandleFunc("PUT /admin/profile-settings", h.auth(h.handleProfileSettingsUpdate, true))

	// data plane (token auth inside the hub)
	mux.HandleFunc("/v1/control", h.svc.Hub.ServeControl)

	// API mount + SPA fallback
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", api))
	mux.Handle("/", h.webUI())
}

// webUI serves the embedded React build, falling back to a JSON hint when the
// bundle is not compiled in.
func (h *httpServer) webUI() http.Handler {
	dir := h.svc.Cfg.WebUI.Dir
	if dir != "" {
		return spaHandler(os.DirFS(dir))
	}
	sub := embeddedWebUI()
	if sub == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") {
				writeError(w, apierr.NotFound("NOT_FOUND", "接口不存在"))
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{
				"service": "yukitunnel",
				"message": "WebUI 资源未打包。开发模式请运行 pnpm dev，或执行 pnpm build 后重新编译。",
			})
		})
	}
	return spaHandler(sub)
}

// spaHandler serves a single-page app from fsys: existing files are served as
// is, unknown asset paths return 404, and every other path falls back to
// index.html so deep links survive a refresh.
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	index := func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			writeError(w, apierr.NotFound("NOT_FOUND", "接口不存在"))
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			index(w, r)
			return
		}
		if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		index(w, r)
	})
}

// withMiddleware adds request IDs, panic recovery, security headers and access
// logging.
func (h *httpServer) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// The desktop app runs on tauri://localhost and talks to a remote server,
		// so the API must be usable cross-origin. Auth is a bearer token (never a
		// cookie), which makes a wildcard origin safe here.
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		defer func() {
			if rec := recover(); rec != nil {
				h.svc.Log.Error("panic serving request", "path", r.URL.Path, "panic", rec)
				writeError(w, apierr.Internal("服务器内部错误"))
			}
			h.svc.Log.Debug("http", "method", r.Method, "path", r.URL.Path,
				"dur", time.Since(start).String(), "remote", clientIP(r))
		}()
		if r.URL.Path == "/api/v1/auth/login" || strings.HasSuffix(r.URL.Path, "/key/login") {
			if !h.loginAllowed(r) {
				writeError(w, apierr.TooMany("尝试过于频繁，请稍后再试"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

var loginBuckets sync.Map

// resetLoginBuckets clears the per-IP login throttle.
//
// The buckets are process-global (they must be: the same client can hit any
// request goroutine), which means every test in this package shares one
// budget. Tests call this so a suite of dozens of logins does not trip the
// limiter and produce confusing 429s.
func resetLoginBuckets() {
	loginBuckets.Range(func(k, _ any) bool {
		loginBuckets.Delete(k)
		return true
	})
}

func (h *httpServer) loginAllowed(r *http.Request) bool {
	ip := clientIP(r)
	now := time.Now()
	v, _ := loginBuckets.LoadOrStore(ip, &bucket{window: now})
	b := v.(*bucket)
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Sub(b.window) > time.Minute {
		b.window = now
		b.count = 0
	}
	b.count++
	return b.count <= 30
}

type bucket struct {
	mu     sync.Mutex
	window time.Time
	count  int
}

// auth wraps a handler with bearer-token authentication and role checks.
func (h *httpServer) auth(fn func(http.ResponseWriter, *http.Request, *store.User), admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _, err := h.svc.Auth.AuthenticateToken(r.Context(), bearerToken(r))
		if err != nil {
			writeError(w, apierr.As(err))
			return
		}
		if admin && !u.IsAdmin() {
			writeError(w, apierr.Forbidden("FORBIDDEN", "需要管理员权限"))
			return
		}
		fn(w, r, u)
	}
}

func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if len(v) > 7 && strings.EqualFold(v[:7], "Bearer ") {
		return v[7:]
	}
	return ""
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, e *apierr.Error) {
	if e == nil {
		e = apierr.Internal("未知错误")
	}
	writeJSON(w, e.Status, map[string]any{"error": e})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		msg := "请求格式错误"
		if errors.Is(err, io.EOF) {
			msg = "请求体不能为空"
		} else {
			msg += "：" + err.Error()
		}
		writeError(w, apierr.BadRequest("BAD_REQUEST", msg))
		return false
	}
	return true
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// slogAdapter lets the hub log through the service logger.
func (s *Service) logHub(level, msg string, kv map[string]any) {
	s.Log.Log(context.Background(), slog.LevelInfo, msg, "detail", kv)
}
