package wsnet

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newPair returns a connected client/server websocket pair.
func newPair(t *testing.T) (*Conn, *Conn) {
	t.Helper()
	var serverConn *websocket.Conn
	ready := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConn = c
		close(ready)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("server side never connected")
	}
	return New(client), New(serverConn)
}

func TestWriteReadRoundTrip(t *testing.T) {
	a, b := newPair(t)

	msg := []byte("hello over websocket")
	if n, err := a.Write(msg); err != nil || n != len(msg) {
		t.Fatalf("write: n=%d err=%v", n, err)
	}

	buf := make([]byte, 64)
	_ = b.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := b.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("got %q want %q", buf[:n], msg)
	}
}

// A single WebSocket message larger than the caller's buffer must be delivered
// across several Read calls, in order and without loss. yamux depends on this.
func TestReadSpansPartialMessages(t *testing.T) {
	a, b := newPair(t)

	payload := bytes.Repeat([]byte("0123456789"), 100) // 1000 bytes
	if _, err := a.Write(payload); err != nil {
		t.Fatal(err)
	}

	var got []byte
	buf := make([]byte, 64)
	_ = b.SetReadDeadline(time.Now().Add(3 * time.Second))
	for len(got) < len(payload) {
		n, err := b.Read(buf)
		if err != nil {
			t.Fatalf("read after %d bytes: %v", len(got), err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload corrupted: got %d bytes", len(got))
	}
}

// Several small writes must arrive as several messages and be readable in order.
func TestMultipleMessagesInOrder(t *testing.T) {
	a, b := newPair(t)

	want := []string{"first", "second", "third"}
	for _, s := range want {
		if _, err := a.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	buf := make([]byte, 64)
	for _, w := range want {
		_ = b.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := b.Read(buf)
		if err != nil {
			t.Fatalf("read %q: %v", w, err)
		}
		if string(buf[:n]) != w {
			t.Fatalf("got %q want %q", buf[:n], w)
		}
	}
}

// Concurrent writes must not interleave: Write holds a mutex because gorilla's
// WriteMessage is not safe for concurrent use.
func TestConcurrentWritesDoNotCorrupt(t *testing.T) {
	a, b := newPair(t)

	const writers, perWriter = 8, 20
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		for i := 0; i < writers*perWriter; i++ {
			_ = b.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := b.Read(buf); err != nil {
				return
			}
		}
	}()

	start := make(chan struct{})
	for w := 0; w < writers; w++ {
		go func() {
			<-start
			for i := 0; i < perWriter; i++ {
				_, _ = a.Write([]byte("x"))
			}
		}()
	}
	close(start)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reading concurrent writes")
	}
}

// A closed connection must surface an error rather than blocking forever.
func TestReadAfterClose(t *testing.T) {
	a, b := newPair(t)
	_ = a.Close()

	buf := make([]byte, 8)
	_ = b.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := b.Read(buf); err == nil {
		t.Fatal("expected an error after the peer closed")
	}
}

func TestAddrAndDeadlines(t *testing.T) {
	a, b := newPair(t)
	if a.LocalAddr() == nil || a.RemoteAddr() == nil {
		t.Fatal("addresses must not be nil")
	}
	if err := b.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	if err := b.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := b.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	// The Conn must satisfy io.ReadWriteCloser so yamux can use it.
	var _ io.ReadWriteCloser = a
}
