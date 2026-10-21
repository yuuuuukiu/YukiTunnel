// Package tunnel implements the YukiTunnel data plane.
//
// One WebSocket connection from a client to the server carries a yamux session.
// The client opens the first stream as a JSON control stream (hello / register /
// stats / stop). Every proxied connection is a new yamux stream whose first line
// is a JSON header describing the target tunnel; the rest of the stream is the
// raw payload. UDP is tunnelled as length-prefixed datagrams inside a stream
// that lives as long as the public source address keeps sending traffic.
package tunnel

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ProtocolVersion is bumped when the control or stream framing changes.
const ProtocolVersion = 1

// Control message types.
const (
	MsgHello      = "hello"
	MsgHelloOK    = "hello_ok"
	MsgRegister   = "register"
	MsgRegistered = "registered"
	MsgStats      = "stats"
	MsgStop       = "stop"
	MsgPing       = "ping"
	MsgPong       = "pong"
	MsgError      = "error"
)

// ControlMessage is the envelope for every control-stream message.
type ControlMessage struct {
	Type    string                 `json:"type"`
	Version int                    `json:"version,omitempty"`
	Client  string                 `json:"client,omitempty"`
	Message string                 `json:"message,omitempty"`
	Code    string                 `json:"code,omitempty"`
	Tunnels []RegisterTunnel       `json:"tunnels,omitempty"`
	Results []RegisterResult       `json:"results,omitempty"`
	Stats   map[string]TunnelStats `json:"stats,omitempty"`
	IDs     []string               `json:"ids,omitempty"`
	Time    time.Time              `json:"time,omitempty"`
}

// RegisterTunnel is the client's request to expose a tunnel.
type RegisterTunnel struct {
	ID         string `json:"id"`
	Proto      string `json:"proto"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	PublicPort int    `json:"public_port"`
	Domain     string `json:"domain"`
	Bandwidth  int    `json:"bandwidth_kbps,omitempty"`
	MaxConns   int    `json:"max_connections,omitempty"`
}

// RegisterResult reports per-tunnel registration outcome.
type RegisterResult struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Address string `json:"address,omitempty"`
}

// TunnelStats is the traffic counter snapshot for one tunnel.
type TunnelStats struct {
	RxBytes int64 `json:"rx_bytes"`
	TxBytes int64 `json:"tx_bytes"`
	Conns   int64 `json:"connections"`
	Active  int   `json:"active_connections"`
}

// StreamHeader is the first line of every proxied yamux stream.
type StreamHeader struct {
	TunnelID   string `json:"tunnel_id"`
	RemoteAddr string `json:"remote_addr"`
	Kind       string `json:"kind"` // tcp | udp | http
	// LocalIP/LocalPort/Proto let the agent forward without holding its own
	// tunnel registry, so a tunnel started from the server (or after a client
	// reconnect) always reaches the right local service.
	LocalIP   string `json:"local_ip,omitempty"`
	LocalPort int    `json:"local_port,omitempty"`
	Proto     string `json:"proto,omitempty"`
}

// ErrUnknownTunnel is returned when a stream references a tunnel that is not
// registered on this agent.
var ErrUnknownTunnel = errors.New("unknown tunnel")

// WriteHeader writes a newline terminated JSON header.
func WriteHeader(w io.Writer, h StreamHeader) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// ReadHeader reads a single newline terminated JSON header.
func ReadHeader(r *bufio.Reader) (StreamHeader, error) {
	var h StreamHeader
	line, err := r.ReadBytes('\n')
	if err != nil {
		return h, err
	}
	if len(line) > 4096 {
		return h, fmt.Errorf("stream header too large")
	}
	if err := json.Unmarshal(line, &h); err != nil {
		return h, fmt.Errorf("bad stream header: %w", err)
	}
	if h.TunnelID == "" {
		return h, errors.New("stream header missing tunnel_id")
	}
	return h, nil
}

// WriteControl writes one JSON line to the control stream.
func WriteControl(w io.Writer, mu *sync.Mutex, msg ControlMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	_, err = w.Write(b)
	return err
}

// ReadControl reads one JSON line from the control stream.
func ReadControl(r *bufio.Reader) (ControlMessage, error) {
	var m ControlMessage
	line, err := r.ReadBytes('\n')
	if err != nil {
		return m, err
	}
	if len(line) > 1<<20 {
		return m, errors.New("control message too large")
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return m, fmt.Errorf("bad control message: %w", err)
	}
	return m, nil
}

// WriteDatagram writes a length-prefixed UDP payload (max 65535 bytes).
func WriteDatagram(w io.Writer, p []byte) error {
	if len(p) > 65535 {
		return errors.New("datagram too large")
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(p)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(p)
	return err
}

// ReadDatagram reads one length-prefixed UDP payload.
func ReadDatagram(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
