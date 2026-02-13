package helper

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/matrix-org/gomatrix"
	"github.com/sony/gobreaker/v2"
	"golang.org/x/time/rate"
)

type RetryOptions struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	UseJitter   bool
}

var DefaultRetryOptions = RetryOptions{
	MaxAttempts: 3,
	BaseDelay:   time.Second,
	MaxDelay:    time.Second * 5,
	UseJitter:   true,
}

type CircuitBreakerOptions struct {
	Threshold     int
	Timeout       time.Duration
	Enabled       bool
	ShouldTrip    func(err error) bool
	OnStateChange func(from, to string)
}

var DefaultCircuitBreakerOptions = CircuitBreakerOptions{
	Threshold:     5,
	Timeout:       time.Minute,
	Enabled:       true,
	ShouldTrip:    func(err error) bool { return true },
	OnStateChange: nil,
}

type RateLimiterOptions struct {
	Rate     int
	Interval time.Duration
	Capacity int
	Enabled  bool
}

var DefaultRateLimiterOptions = RateLimiterOptions{
	Rate:     60,
	Interval: time.Minute,
	Capacity: 60,
	Enabled:  true,
}

type RateLimitError struct {
	ErrCode      string `json:"errcode"`
	Error        string `json:"error"`
	RetryAfterMs int64  `json:"retry_after_ms"`
}

// DoOption configures a single Do call.
type doConfig struct {
	skipRateLimit bool
}

type DoOption func(*doConfig)

// WithoutRateLimit skips the rate limiter for this call.
func WithoutRateLimit() DoOption {
	return func(c *doConfig) {
		c.skipRateLimit = true
	}
}

// smartBackOff wraps an ExponentialBackOff and overrides the delay
// when a 429 response includes retry_after_ms.
// Rate-limit retries (429) do not count toward maxRetries, allowing
// the operation to keep retrying as long as the server asks to wait.
// A separate maxRateLimitRetries cap prevents infinite loops.
type smartBackOff struct {
	inner              backoff.BackOff
	lastErr            *error
	maxRetries         uint64
	numRetries         uint64
	maxRateLimitRetries uint64
	numRateLimitRetries uint64
}

func (s *smartBackOff) NextBackOff() time.Duration {
	if s.lastErr != nil && *s.lastErr != nil {
		if delay := extractRetryAfter(*s.lastErr); delay > 0 {
			s.numRateLimitRetries++
			if s.maxRateLimitRetries > 0 && s.numRateLimitRetries > s.maxRateLimitRetries {
				return backoff.Stop
			}
			return delay
		}
	}

	s.numRetries++
	if s.numRetries > s.maxRetries {
		return backoff.Stop
	}

	return s.inner.NextBackOff()
}

func (s *smartBackOff) Reset() {
	s.inner.Reset()
	s.numRetries = 0
	s.numRateLimitRetries = 0
}

// extractRetryAfter parses retry_after_ms from a 429 HTTP error response.
func extractRetryAfter(err error) time.Duration {
	var httpErr *gomatrix.HTTPError
	if errors.As(err, &httpErr) && httpErr.Code == 429 && httpErr.Contents != nil {
		var rateLimit RateLimitError
		if json.Unmarshal(httpErr.Contents, &rateLimit) == nil && rateLimit.RetryAfterMs > 0 {
			serverDelay := time.Duration(rateLimit.RetryAfterMs) * time.Millisecond
			return time.Duration(float64(serverDelay) * 1.1)
		}
	}
	return 0
}

// Retry combines retry with exponential backoff, circuit breaker, and rate limiting.
// The pausedUntil field provides cross-goroutine coordination: when any goroutine
// receives a 429, all goroutines pause until the server-specified deadline.
type Retry struct {
	retryOpts   RetryOptions
	cb          *gobreaker.CircuitBreaker[struct{}]
	rateLimiter *rate.Limiter
	pausedUntil atomic.Int64 // unix nanoseconds; 0 = not paused
}

// setPause records a server-requested pause. If the new deadline is later
// than the current one, it wins (concurrent 429s → longest pause prevails).
func (r *Retry) setPause(d time.Duration) {
	deadline := time.Now().Add(d).UnixNano()
	for {
		current := r.pausedUntil.Load()
		if deadline <= current {
			return
		}
		if r.pausedUntil.CompareAndSwap(current, deadline) {
			return
		}
	}
}

// waitForPause blocks until the shared pause expires or the context is cancelled.
func (r *Retry) waitForPause(ctx context.Context) error {
	deadline := r.pausedUntil.Load()
	if deadline == 0 {
		return nil
	}
	remaining := time.Until(time.Unix(0, deadline))
	if remaining <= 0 {
		return nil
	}
	select {
	case <-time.After(remaining):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NewRetry(retryOpts RetryOptions, cbOpts CircuitBreakerOptions, rlOpts RateLimiterOptions) *Retry {
	r := &Retry{
		retryOpts: retryOpts,
	}

	if cbOpts.Enabled {
		settings := gobreaker.Settings{
			Name:    "matrix-helper",
			Timeout: cbOpts.Timeout,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				return int(counts.ConsecutiveFailures) >= cbOpts.Threshold
			},
			IsSuccessful: func(err error) bool {
				if err == nil {
					return true
				}
				if cbOpts.ShouldTrip != nil {
					return !cbOpts.ShouldTrip(err)
				}
				return false
			},
		}
		if cbOpts.OnStateChange != nil {
			settings.OnStateChange = func(name string, from, to gobreaker.State) {
				cbOpts.OnStateChange(from.String(), to.String())
			}
		}
		r.cb = gobreaker.NewCircuitBreaker[struct{}](settings)
	}

	if rlOpts.Enabled {
		tokensPerSecond := rate.Limit(float64(rlOpts.Rate) / rlOpts.Interval.Seconds())
		r.rateLimiter = rate.NewLimiter(tokensPerSecond, rlOpts.Capacity)
	}

	return r
}

// Do executes the operation with retry, circuit breaker, and rate limiting.
func (r *Retry) Do(ctx context.Context, operation func() error, opts ...DoOption) error {
	cfg := &doConfig{}
	for _, o := range opts {
		o(cfg)
	}

	// Rate limiter: wait for a token before proceeding
	if r.rateLimiter != nil && !cfg.skipRateLimit {
		if err := r.rateLimiter.Wait(ctx); err != nil {
			return err
		}
	}

	// Set up exponential backoff
	var lastErr error
	eb := backoff.NewExponentialBackOff()
	eb.InitialInterval = r.retryOpts.BaseDelay
	eb.MaxInterval = r.retryOpts.MaxDelay
	eb.MaxElapsedTime = 0 // no total time limit; controlled by max retries
	if r.retryOpts.UseJitter {
		eb.RandomizationFactor = 0.2
	} else {
		eb.RandomizationFactor = 0
	}

	smart := &smartBackOff{
		inner:               eb,
		lastErr:             &lastErr,
		maxRetries:          uint64(max(r.retryOpts.MaxAttempts-1, 0)),
		maxRateLimitRetries: 10,
	}
	bCtx := backoff.WithContext(smart, ctx)

	wrappedOp := func() error {
		// Wait for any shared pause set by another goroutine's 429.
		if err := r.waitForPause(ctx); err != nil {
			return backoff.Permanent(err)
		}

		var err error
		if r.cb != nil {
			_, err = r.cb.Execute(func() (struct{}, error) {
				return struct{}{}, operation()
			})
			if err != nil {
				if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
					lastErr = err
					return backoff.Permanent(err)
				}
			}
		} else {
			err = operation()
		}

		lastErr = err

		// If this was a 429, pause ALL goroutines for the server-specified duration.
		if err != nil {
			if delay := extractRetryAfter(err); delay > 0 {
				r.setPause(delay)
			}
		}

		return err
	}

	return backoff.Retry(wrappedOp, bCtx)
}
