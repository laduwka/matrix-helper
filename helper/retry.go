package helper

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/matrix-org/gomatrix"
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

type CircuitBreaker struct {
	mutex         sync.RWMutex
	state         string
	failures      int
	lastFailure   time.Time
	threshold     int
	timeout       time.Duration
	shouldTrip    func(err error) bool
	onStateChange func(from, to string)
}

func NewCircuitBreaker(opts CircuitBreakerOptions) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:         "closed",
		failures:      0,
		threshold:     opts.Threshold,
		timeout:       opts.Timeout,
		shouldTrip:    opts.ShouldTrip,
		onStateChange: opts.OnStateChange,
	}
	return cb
}

func (cb *CircuitBreaker) Allow() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state != "open"
}

func (cb *CircuitBreaker) Success() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	prevState := cb.state

	if cb.state == "half-open" {
		cb.state = "closed"
		cb.failures = 0
	}

	if cb.state == "closed" {
		cb.failures = 0
	}

	if cb.onStateChange != nil && prevState != cb.state {
		cb.onStateChange(prevState, cb.state)
	}
}

func (cb *CircuitBreaker) Failure(err error) {
	if !cb.shouldTrip(err) {
		return
	}

	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	prevState := cb.state

	cb.failures++
	cb.lastFailure = time.Now()

	if cb.state == "closed" && cb.failures >= cb.threshold {
		cb.state = "open"
	}

	if cb.state == "half-open" {
		cb.state = "open"
	}

	if cb.onStateChange != nil && prevState != cb.state {
		cb.onStateChange(prevState, cb.state)
	}
}

func (cb *CircuitBreaker) CheckState() string {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if cb.state == "open" && time.Since(cb.lastFailure) > cb.timeout {
		prevState := cb.state
		cb.state = "half-open"
		if cb.onStateChange != nil {
			cb.onStateChange(prevState, cb.state)
		}
	}

	return cb.state
}

func (cb *CircuitBreaker) State() string {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}

type TokenBucket struct {
	mutex      sync.Mutex
	tokens     float64
	rate       float64
	capacity   float64
	lastRefill time.Time
}

func NewTokenBucket(opts RateLimiterOptions) *TokenBucket {
	tokensPerSecond := float64(opts.Rate) / opts.Interval.Seconds()
	return &TokenBucket{
		tokens:     float64(opts.Capacity),
		rate:       tokensPerSecond,
		capacity:   float64(opts.Capacity),
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) Allow() bool {
	tb.mutex.Lock()
	defer tb.mutex.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.lastRefill = now

	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}

	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}
	return false
}

type Retry struct {
	retryOpts      RetryOptions
	circuitBreaker *CircuitBreaker
	rateLimiter    *TokenBucket
}

func NewRetry(retryOpts RetryOptions, cbOpts CircuitBreakerOptions, rlOpts RateLimiterOptions) *Retry {
	r := &Retry{
		retryOpts: retryOpts,
	}

	if cbOpts.Enabled {
		r.circuitBreaker = NewCircuitBreaker(cbOpts)
	}

	if rlOpts.Enabled {
		r.rateLimiter = NewTokenBucket(rlOpts)
	}

	return r
}

func (r *Retry) Do(ctx context.Context, operation func() error) error {
	var err error

	if r.circuitBreaker != nil {
		r.circuitBreaker.CheckState()
		if !r.circuitBreaker.Allow() {
			return errors.New("circuit breaker is open")
		}
	}

	if r.rateLimiter != nil {
		if !r.rateLimiter.Allow() {
			return errors.New("rate limit exceeded")
		}
	}

	for attempt := 0; attempt < r.retryOpts.MaxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err = operation(); err == nil {

				if r.circuitBreaker != nil {
					r.circuitBreaker.Success()
				}
				return nil
			}

			if r.circuitBreaker != nil {
				r.circuitBreaker.Failure(err)
			}

			if attempt == r.retryOpts.MaxAttempts-1 {
				return err
			}

			delay := r.calculateSmartDelay(err, attempt)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return err
}

func (r *Retry) calculateSmartDelay(err error, attempt int) time.Duration {

	delay := r.retryOpts.BaseDelay * time.Duration(1<<uint(attempt))
	if delay > r.retryOpts.MaxDelay {
		delay = r.retryOpts.MaxDelay
	}

	if r.retryOpts.UseJitter {
		jitter := 0.8 + (0.4 * rand.Float64())
		delayNs := float64(delay.Nanoseconds())
		delay = time.Duration(int64(delayNs * jitter))
	}

	var httpErr *gomatrix.HTTPError
	if errors.As(err, &httpErr) && httpErr.Code == 429 {

		if httpErr.Contents != nil {
			var rateLimit RateLimitError
			if err := json.Unmarshal(httpErr.Contents, &rateLimit); err == nil {
				if rateLimit.RetryAfterMs > 0 {
					serverDelay := time.Duration(rateLimit.RetryAfterMs) * time.Millisecond

					return time.Duration(float64(serverDelay) * 1.1)
				}
			}
		}
	}

	return delay
}
