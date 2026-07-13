// Package retry implements error classification and exponential backoff
// with jitter for steamcmd and Steam Web API operations (spec.md section 5).
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Class says whether and how an error should be retried.
type Class int

const (
	// Retryable gets exponential backoff: CDN timeouts, generic steamcmd
	// "Failure", HTTP 429/5xx from the Web API. Unmarked errors default
	// here — the attempt cap still bounds them.
	Retryable Class = iota
	// Fatal is never retried: "No subscription", "Invalid password".
	Fatal
	// RateLimited is retried only after a heavily increased flat delay:
	// "Rate Limit Exceeded" on login.
	RateLimited
)

type classified struct {
	err   error
	class Class
}

func (c *classified) Error() string { return c.err.Error() }
func (c *classified) Unwrap() error { return c.err }

// Mark attaches a retry class to err. Mark(nil, ...) is nil.
func Mark(err error, class Class) error {
	if err == nil {
		return nil
	}
	return &classified{err: err, class: class}
}

// ClassOf extracts the class attached anywhere in err's chain;
// unmarked errors are Retryable.
func ClassOf(err error) Class {
	var c *classified
	if errors.As(err, &c) {
		return c.class
	}
	return Retryable
}

// Policy is an exponential backoff with jitter. Build one from a
// Default*Policy and override fields from config.
type Policy struct {
	MaxAttempts    int
	BaseDelay      time.Duration
	MaxDelay       time.Duration
	Multiplier     float64
	Jitter         float64       // ± fraction of the delay, 0..1
	RateLimitDelay time.Duration // flat delay for RateLimited errors

	// Sleep and Rand are test hooks; nil means a real timer and math/rand.
	Sleep func(context.Context, time.Duration) error
	Rand  func() float64
}

// Per-operation defaults (spec section 5 wants these separately tunable).

func DefaultDownloadPolicy() Policy {
	// MaxAttempts 1 = no automatic download retries by default (owner's
	// decision): a failed item is picked up by the next sync anyway. Opt in
	// via retry.download.max_attempts; the delays below then apply.
	return Policy{MaxAttempts: 1, BaseDelay: 5 * time.Second, MaxDelay: 2 * time.Minute,
		Multiplier: 2, Jitter: 0.2, RateLimitDelay: 15 * time.Minute}
}

func DefaultWebAPIPolicy() Policy {
	return Policy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: 30 * time.Second,
		Multiplier: 2, Jitter: 0.2, RateLimitDelay: 5 * time.Minute}
}

func DefaultLoginPolicy() Policy {
	return Policy{MaxAttempts: 3, BaseDelay: 10 * time.Second, MaxDelay: 5 * time.Minute,
		Multiplier: 2, Jitter: 0.2, RateLimitDelay: 15 * time.Minute}
}

// Do runs op, retrying according to the policy. Fatal errors return
// immediately; Retryable ones back off exponentially with jitter;
// RateLimited ones wait the flat RateLimitDelay. Once the attempt cap is
// reached the last error is returned.
func (p Policy) Do(ctx context.Context, op func(context.Context) error) error {
	attempts := p.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; ; attempt++ {
		err := op(ctx)
		if err == nil {
			return nil
		}
		if ClassOf(err) == Fatal {
			return err
		}
		if attempt >= attempts {
			if attempts > 1 {
				return fmt.Errorf("giving up after %d attempts: %w", attempts, err)
			}
			return err
		}
		if serr := p.sleep(ctx, p.delay(attempt, ClassOf(err))); serr != nil {
			return serr
		}
	}
}

func (p Policy) delay(attempt int, class Class) time.Duration {
	if class == RateLimited && p.RateLimitDelay > 0 {
		return p.RateLimitDelay
	}
	mult := p.Multiplier
	if mult < 1 {
		mult = 2
	}
	d := float64(p.BaseDelay)
	for i := 1; i < attempt; i++ {
		d *= mult
		if p.MaxDelay > 0 && d >= float64(p.MaxDelay) {
			d = float64(p.MaxDelay)
			break
		}
	}
	if j := p.Jitter; j > 0 {
		random := rand.Float64
		if p.Rand != nil {
			random = p.Rand
		}
		d *= 1 + j*(2*random()-1)
	}
	return time.Duration(d)
}

func (p Policy) sleep(ctx context.Context, d time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
