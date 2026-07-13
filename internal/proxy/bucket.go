package proxy

import (
	"sync"
	"time"
)

// bucket is a token bucket over bytes. All proxy connections share one
// bucket, so the limit caps the total transfer rate without splitting it
// between workers (spec section 9).
type bucket struct {
	rate  float64 // bytes per second; <= 0 means unlimited
	burst float64

	mu     sync.Mutex
	tokens float64
	last   time.Time

	// test hooks
	now   func() time.Time
	sleep func(time.Duration)
}

func newBucket(bytesPerSec int64) *bucket {
	burst := float64(bytesPerSec) // one second worth of traffic
	if burst < 64<<10 {
		burst = 64 << 10
	}
	b := &bucket{
		rate:  float64(bytesPerSec),
		burst: burst,
		now:   time.Now,
		sleep: time.Sleep,
	}
	b.tokens = burst
	b.last = b.now()
	return b
}

// wait blocks until n bytes may pass.
func (b *bucket) wait(n int) {
	if b.rate <= 0 {
		return
	}
	b.mu.Lock()
	now := b.now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
	b.tokens -= float64(n)
	var wait time.Duration
	if b.tokens < 0 {
		wait = time.Duration(-b.tokens / b.rate * float64(time.Second))
	}
	b.mu.Unlock()
	if wait > 0 {
		b.sleep(wait)
	}
}
