package helper

import (
	"context"
	"encoding/json"
	"errors"
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
type smartBackOff struct {
	inner   backoff.BackOff
	lastErr *error
}

func (s *smartBackOff) NextBackOff() time.Duration {
	base := s.inner.NextBackOff()
	if base == backoff.Stop {
		return backoff.Stop
	}

	if s.lastErr != nil && *s.lastErr != nil {
		if delay := extractRetryAfter(*s.lastErr); delay > 0 {
			return delay
		}
	}

	return base
}

func (s *smartBackOff) Reset() {
	s.inner.Reset()
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
type Retry struct {
	retryOpts   RetryOptions
	cb          *gobreaker.CircuitBreaker[struct{}]
	rateLimiter *rate.Limiter
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

	smart := &smartBackOff{inner: eb, lastErr: &lastErr}
	maxRetries := backoff.WithMaxRetries(smart, uint64(r.retryOpts.MaxAttempts-1))
	bCtx := backoff.WithContext(maxRetries, ctx)

	wrappedOp := func() error {
		if r.cb != nil {
			_, err := r.cb.Execute(func() (struct{}, error) {
				return struct{}{}, operation()
			})
			lastErr = err
			if err != nil {
				if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
					return backoff.Permanent(err)
				}
			}
			return err
		}
		err := operation()
		lastErr = err
		return err
	}

	return backoff.Retry(wrappedOp, bCtx)
}
