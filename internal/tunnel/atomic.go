package tunnel

import "sync/atomic"

func loadInt64(p *int64) int64                      { return atomic.LoadInt64(p) }
func compareAndSwapInt64(p *int64, o, n int64) bool { return atomic.CompareAndSwapInt64(p, o, n) }
func addInt64(p *int64, n int64)                    { atomic.AddInt64(p, n) }
func loadInt32(p *int32) int32                      { return atomic.LoadInt32(p) }
func addInt32(p *int32, n int32)                    { atomic.AddInt32(p, n) }
