package tunnel

import (
	"io"
	"sync"
	"time"
)

// rateLimiter is a minimal token bucket used for per-tunnel bandwidth caps.
type rateLimiter struct {
	mu     sync.Mutex
	rate   float64 // bytes per second
	burst  float64
	tokens float64
	last   time.Time
}

func newRateLimiter(kbps int) *rateLimiter {
	if kbps <= 0 {
		return nil
	}
	rate := float64(kbps) * 1024 / 8 // kilobits -> bytes
	return &rateLimiter{rate: rate, burst: rate, tokens: rate, last: time.Now()}
}

// wait blocks until n tokens are available or the deadline passes.
func (l *rateLimiter) wait(n int) {
	if l == nil || n <= 0 {
		return
	}
	for {
		l.mu.Lock()
		now := time.Now()
		l.tokens += now.Sub(l.last).Seconds() * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
		if l.tokens >= float64(n) {
			l.tokens -= float64(n)
			l.mu.Unlock()
			return
		}
		need := float64(n) - l.tokens
		wait := time.Duration(need / l.rate * float64(time.Second))
		l.mu.Unlock()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		time.Sleep(wait)
	}
}

// limitedReader throttles reads.
type limitedReader struct {
	r io.Reader
	l *rateLimiter
}

func (r *limitedReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.l.wait(n)
	}
	return n, err
}

// limitedWriter throttles writes.
type limitedWriter struct {
	w io.Writer
	l *rateLimiter
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.l.wait(len(p))
	return w.w.Write(p)
}

// limitCopy copies src to dst counting bytes, optionally throttled by kbps.
func limitCopy(dst io.Writer, src io.Reader, kbps int, counter *int64) (int64, error) {
	var r io.Reader = src
	var w io.Writer = dst
	if l := newRateLimiter(kbps); l != nil {
		r = &limitedReader{r: src, l: l}
		w = &limitedWriter{w: dst, l: l}
	}
	n, err := io.Copy(w, r)
	if counter != nil {
		atomicAdd(counter, n)
	}
	return n, err
}

func atomicAdd(p *int64, n int64) {
	// kept in a helper so callers do not need sync/atomic imports
	for {
		old := loadInt64(p)
		if compareAndSwapInt64(p, old, old+n) {
			return
		}
	}
}
