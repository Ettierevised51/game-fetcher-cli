package retry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func testPolicy(delays *[]time.Duration) Policy {
	return Policy{
		MaxAttempts: 4, BaseDelay: time.Second, MaxDelay: 10 * time.Second,
		Multiplier: 2, Jitter: 0, RateLimitDelay: time.Minute,
		Sleep: func(_ context.Context, d time.Duration) error {
			*delays = append(*delays, d)
			return nil
		},
	}
}

func TestClassOf(t *testing.T) {
	if got := ClassOf(errors.New("plain")); got != Retryable {
		t.Errorf("unmarked error: ClassOf = %v, want Retryable", got)
	}
	wrapped := fmt.Errorf("context: %w", Mark(errors.New("no sub"), Fatal))
	if got := ClassOf(wrapped); got != Fatal {
		t.Errorf("wrapped fatal: ClassOf = %v, want Fatal", got)
	}
	if Mark(nil, Fatal) != nil {
		t.Error("Mark(nil) must be nil")
	}
}

func TestDoFatalStopsImmediately(t *testing.T) {
	var delays []time.Duration
	calls := 0
	err := testPolicy(&delays).Do(context.Background(), func(context.Context) error {
		calls++
		return Mark(errors.New("no subscription"), Fatal)
	})
	if err == nil || calls != 1 || len(delays) != 0 {
		t.Fatalf("fatal error: calls=%d delays=%v err=%v; want 1 call, no sleeps", calls, delays, err)
	}
}

func TestDoRetryableBacksOffExponentially(t *testing.T) {
	var delays []time.Duration
	calls := 0
	err := testPolicy(&delays).Do(context.Background(), func(context.Context) error {
		calls++
		if calls < 4 {
			return errors.New("timeout")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if calls != 4 || len(delays) != 3 {
		t.Fatalf("calls=%d delays=%v", calls, delays)
	}
	for i, d := range delays {
		if d != want[i] {
			t.Errorf("delay[%d] = %v, want %v", i, d, want[i])
		}
	}
}

func TestDoStopsAtAttemptCap(t *testing.T) {
	var delays []time.Duration
	calls := 0
	err := testPolicy(&delays).Do(context.Background(), func(context.Context) error {
		calls++
		return errors.New("still broken")
	})
	if calls != 4 {
		t.Errorf("calls = %d, want 4 (MaxAttempts)", calls)
	}
	if err == nil || !strings.Contains(err.Error(), "4 attempts") {
		t.Errorf("error should mention the attempt cap, got: %v", err)
	}
}

func TestDoRateLimitedUsesFlatDelay(t *testing.T) {
	var delays []time.Duration
	calls := 0
	err := testPolicy(&delays).Do(context.Background(), func(context.Context) error {
		calls++
		if calls == 1 {
			return Mark(errors.New("rate limit exceeded"), RateLimited)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delays) != 1 || delays[0] != time.Minute {
		t.Errorf("delays = %v, want the flat RateLimitDelay of 1m", delays)
	}
}

func TestDelayJitterBounds(t *testing.T) {
	p := Policy{BaseDelay: time.Second, MaxDelay: time.Minute, Multiplier: 2, Jitter: 0.5}
	p.Rand = func() float64 { return 1 }
	if d := p.delay(1, Retryable); d != 1500*time.Millisecond {
		t.Errorf("upper jitter: %v, want 1.5s", d)
	}
	p.Rand = func() float64 { return 0 }
	if d := p.delay(1, Retryable); d != 500*time.Millisecond {
		t.Errorf("lower jitter: %v, want 0.5s", d)
	}
}
