// Package wsnet adapts a WebSocket into a net.Conn byte stream so that yamux
// (or any other stream muxer) can run on top of it.
package wsnet

import (
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Conn wraps a WebSocket connection.
type Conn struct {
	ws  *websocket.Conn
	buf []byte
	wmu sync.Mutex
}

// New wraps ws.
func New(ws *websocket.Conn) *Conn { return &Conn{ws: ws} }

// Read returns the next chunk of the byte stream.
func (c *Conn) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return 0, err
		}
		c.buf = data
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

// Write sends p as one binary WebSocket message.
func (c *Conn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close closes the underlying WebSocket.
func (c *Conn) Close() error { return c.ws.Close() }

// LocalAddr returns the local address.
func (c *Conn) LocalAddr() net.Addr { return c.ws.LocalAddr() }

// RemoteAddr returns the peer address.
func (c *Conn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

// SetDeadline sets both read and write deadlines.
func (c *Conn) SetDeadline(t time.Time) error { return c.ws.SetReadDeadline(t) }

// SetReadDeadline sets the read deadline.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.ws.SetReadDeadline(t) }

// SetWriteDeadline sets the write deadline.
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }
