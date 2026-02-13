package helper

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/matrix-org/gomatrix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRetry(retryOpts RetryOptions, cbOpts CircuitBreakerOptions, rlOpts RateLimiterOptions) *Retry {
	return NewRetry(retryOpts, cbOpts, rlOpts)
}

func TestRetryDo_Success(t *testing.T) {
	r := newTestRetry(DefaultRetryOptions, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	callCount := 0
	err := r.Do(context.Background(), func() error {
		callCount++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)
}

func TestRetryDo_RetryThenSuccess(t *testing.T) {
	opts := RetryOptions{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
		UseJitter:   false,
	}
	r := newTestRetry(opts, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	callCount := 0
	err := r.Do(context.Background(), func() error {
		callCount++
		if callCount < 3 {
			return errors.New("temporary error")
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, callCount)
}

func TestRetryDo_AllAttemptsExhausted(t *testing.T) {
	opts := RetryOptions{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
		UseJitter:   false,
	}
	r := newTestRetry(opts, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	callCount := 0
	err := r.Do(context.Background(), func() error {
		callCount++
		return errors.New("persistent error")
	})

	assert.Error(t, err)
	assert.Equal(t, 3, callCount)
	assert.Contains(t, err.Error(), "persistent error")
}

func TestRetryDo_ContextCancelled(t *testing.T) {
	opts := RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   time.Second,
		MaxDelay:    5 * time.Second,
		UseJitter:   false,
	}
	r := newTestRetry(opts, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := r.Do(ctx, func() error {
		callCount++
		return errors.New("error")
	})

	assert.Error(t, err)
	assert.True(t, callCount >= 1)
}

func TestRetryDo_RateLimiterBlocks(t *testing.T) {
	// With burst=0, rate.Limiter.Wait returns an error immediately
	rlOpts := RateLimiterOptions{
		Rate:     1,
		Interval: time.Hour,
		Capacity: 0,
		Enabled:  true,
	}
	r := newTestRetry(DefaultRetryOptions, CircuitBreakerOptions{Enabled: false}, rlOpts)

	err := r.Do(context.Background(), func() error {
		return nil
	})

	assert.Error(t, err)
}

func TestRetryDo_WithoutRateLimit(t *testing.T) {
	// With burst=0 the rate limiter would normally block, but WithoutRateLimit skips it
	rlOpts := RateLimiterOptions{
		Rate:     1,
		Interval: time.Hour,
		Capacity: 0,
		Enabled:  true,
	}
	r := newTestRetry(DefaultRetryOptions, CircuitBreakerOptions{Enabled: false}, rlOpts)

	err := r.Do(context.Background(), func() error {
		return nil
	}, WithoutRateLimit())

	assert.NoError(t, err)
}

func TestRetryDo_CircuitBreakerOpen(t *testing.T) {
	cbOpts := CircuitBreakerOptions{
		Threshold:  1,
		Timeout:    time.Minute,
		Enabled:    true,
		ShouldTrip: func(err error) bool { return true },
	}
	opts := RetryOptions{
		MaxAttempts: 1,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		UseJitter:   false,
	}
	r := newTestRetry(opts, cbOpts, RateLimiterOptions{Enabled: false})

	// Trip the circuit breaker
	_ = r.Do(context.Background(), func() error {
		return errors.New("error")
	})

	// Now circuit breaker should be open
	err := r.Do(context.Background(), func() error {
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker is open")
}

func TestRetryDo_CircuitBreakerExcludesRateLimitErrors(t *testing.T) {
	cbOpts := CircuitBreakerOptions{
		Threshold: 1,
		Timeout:   time.Minute,
		Enabled:   true,
		ShouldTrip: func(err error) bool {
			return err.Error() != "rate limited"
		},
	}
	opts := RetryOptions{
		MaxAttempts: 1,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		UseJitter:   false,
	}
	r := newTestRetry(opts, cbOpts, RateLimiterOptions{Enabled: false})

	// Rate limit error should not trip CB
	_ = r.Do(context.Background(), func() error {
		return errors.New("rate limited")
	})

	// CB should still be closed; operation should execute
	callCount := 0
	err := r.Do(context.Background(), func() error {
		callCount++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, callCount)
}

func TestRetryDo_CircuitBreakerTripsOnOtherErrors(t *testing.T) {
	cbOpts := CircuitBreakerOptions{
		Threshold: 1,
		Timeout:   time.Minute,
		Enabled:   true,
		ShouldTrip: func(err error) bool {
			return err.Error() != "rate limited"
		},
	}
	opts := RetryOptions{
		MaxAttempts: 1,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		UseJitter:   false,
	}
	r := newTestRetry(opts, cbOpts, RateLimiterOptions{Enabled: false})

	// Server error should trip CB
	_ = r.Do(context.Background(), func() error {
		return errors.New("server error")
	})

	// CB should now be open
	err := r.Do(context.Background(), func() error {
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker is open")
}

// extractRetryAfter tests

func TestExtractRetryAfter_429WithRetryAfterMs(t *testing.T) {
	rateErr := RateLimitError{
		ErrCode:      "M_LIMIT_EXCEEDED",
		RetryAfterMs: 5000,
	}
	body, err := json.Marshal(rateErr)
	require.NoError(t, err)

	httpErr := &gomatrix.HTTPError{
		Code:     429,
		Contents: body,
	}

	delay := extractRetryAfter(httpErr)
	expected := time.Duration(float64(5000*time.Millisecond) * 1.1)
	assert.Equal(t, expected, delay)
}

func TestExtractRetryAfter_Non429(t *testing.T) {
	httpErr := &gomatrix.HTTPError{
		Code: 500,
	}
	delay := extractRetryAfter(httpErr)
	assert.Equal(t, time.Duration(0), delay)
}

func TestExtractRetryAfter_429WithoutRetryAfterMs(t *testing.T) {
	httpErr := &gomatrix.HTTPError{
		Code:     429,
		Contents: []byte(`{"errcode": "M_LIMIT_EXCEEDED"}`),
	}
	delay := extractRetryAfter(httpErr)
	assert.Equal(t, time.Duration(0), delay)
}

func TestExtractRetryAfter_RegularError(t *testing.T) {
	delay := extractRetryAfter(errors.New("some error"))
	assert.Equal(t, time.Duration(0), delay)
}

// smartBackOff tests

func TestSmartBackOff_UsesServerDelayFor429(t *testing.T) {
	rateErr := RateLimitError{
		ErrCode:      "M_LIMIT_EXCEEDED",
		RetryAfterMs: 3000,
	}
	body, _ := json.Marshal(rateErr)
	httpErr := &gomatrix.HTTPError{Code: 429, Contents: body}
	var lastErr error = httpErr

	inner := &mockBackOff{nextDelay: 100 * time.Millisecond}
	sb := &smartBackOff{inner: inner, lastErr: &lastErr}

	delay := sb.NextBackOff()
	serverDelay := time.Duration(3000) * time.Millisecond
	expected := time.Duration(float64(serverDelay) * 1.1)
	assert.Equal(t, expected, delay)
}

func TestSmartBackOff_FallsBackToInnerForNon429(t *testing.T) {
	var lastErr error = errors.New("some error")
	inner := &mockBackOff{nextDelay: 200 * time.Millisecond}
	sb := &smartBackOff{inner: inner, lastErr: &lastErr, maxRetries: 2}

	delay := sb.NextBackOff()
	assert.Equal(t, 200*time.Millisecond, delay)
}

func TestSmartBackOff_RateLimitRetriesDoNotCountTowardMax(t *testing.T) {
	rateErr := RateLimitError{
		ErrCode:      "M_LIMIT_EXCEEDED",
		RetryAfterMs: 100,
	}
	body, _ := json.Marshal(rateErr)
	httpErr := &gomatrix.HTTPError{Code: 429, Contents: body}
	var lastErr error = httpErr

	inner := &mockBackOff{nextDelay: 50 * time.Millisecond}
	sb := &smartBackOff{
		inner:               inner,
		lastErr:             &lastErr,
		maxRetries:          1,
		maxRateLimitRetries: 10,
	}

	// First call with 429 — should return server delay, not count toward maxRetries
	delay := sb.NextBackOff()
	assert.True(t, delay > 0 && delay != backoff.Stop, "should return server delay for 429")
	assert.Equal(t, uint64(1), sb.numRateLimitRetries)
	assert.Equal(t, uint64(0), sb.numRetries)

	// Switch to a regular error
	lastErr = errors.New("some error")

	// Should still allow one normal retry (maxRetries=1)
	delay = sb.NextBackOff()
	assert.Equal(t, 50*time.Millisecond, delay)
	assert.Equal(t, uint64(1), sb.numRetries)

	// Second normal retry should stop (exceeded maxRetries=1)
	delay = sb.NextBackOff()
	assert.Equal(t, backoff.Stop, delay)
}

func TestSmartBackOff_RateLimitRetriesCapped(t *testing.T) {
	rateErr := RateLimitError{
		ErrCode:      "M_LIMIT_EXCEEDED",
		RetryAfterMs: 100,
	}
	body, _ := json.Marshal(rateErr)
	httpErr := &gomatrix.HTTPError{Code: 429, Contents: body}
	var lastErr error = httpErr

	inner := &mockBackOff{nextDelay: 50 * time.Millisecond}
	sb := &smartBackOff{
		inner:               inner,
		lastErr:             &lastErr,
		maxRetries:          2,
		maxRateLimitRetries: 2,
	}

	// Two rate-limit retries should succeed
	for i := 0; i < 2; i++ {
		delay := sb.NextBackOff()
		assert.True(t, delay > 0 && delay != backoff.Stop)
	}

	// Third rate-limit retry should stop
	delay := sb.NextBackOff()
	assert.Equal(t, backoff.Stop, delay)
}

type mockBackOff struct {
	nextDelay time.Duration
}

func (m *mockBackOff) NextBackOff() time.Duration {
	return m.nextDelay
}

func (m *mockBackOff) Reset() {}

// Shared pause tests

func make429Error(retryAfterMs int64) error {
	body, _ := json.Marshal(RateLimitError{
		ErrCode:      "M_LIMIT_EXCEEDED",
		RetryAfterMs: retryAfterMs,
	})
	return &gomatrix.HTTPError{Code: 429, Contents: body}
}

func TestRetry_SharedPause_BlocksOtherGoroutines(t *testing.T) {
	opts := RetryOptions{
		MaxAttempts: 2,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
		UseJitter:   false,
	}
	r := newTestRetry(opts, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	var goroutine2Start time.Time
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine 1: returns 429 with 200ms pause, then succeeds
	go func() {
		defer wg.Done()
		callCount := 0
		_ = r.Do(context.Background(), func() error {
			callCount++
			if callCount == 1 {
				return make429Error(200) // 200ms pause
			}
			return nil
		})
	}()

	// Small delay so goroutine 1 sets the pause first
	time.Sleep(20 * time.Millisecond)

	// Goroutine 2: should be delayed by the shared pause
	go func() {
		defer wg.Done()
		_ = r.Do(context.Background(), func() error {
			mu.Lock()
			goroutine2Start = time.Now()
			mu.Unlock()
			return nil
		})
	}()

	before := time.Now()
	wg.Wait()

	mu.Lock()
	elapsed := goroutine2Start.Sub(before)
	mu.Unlock()

	// Goroutine 2 should have waited at least ~150ms (200ms * 1.1 minus the 20ms head start and some margin)
	assert.True(t, elapsed >= 100*time.Millisecond,
		"goroutine 2 should have been delayed by shared pause, elapsed: %v", elapsed)
}

func TestRetry_SharedPause_LatestDeadlineWins(t *testing.T) {
	r := newTestRetry(DefaultRetryOptions, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	// Set a short pause
	r.setPause(50 * time.Millisecond)
	first := r.pausedUntil.Load()

	// Set a longer pause — should win
	r.setPause(200 * time.Millisecond)
	second := r.pausedUntil.Load()

	assert.True(t, second > first, "longer pause should replace shorter one")

	// Set a shorter pause — should NOT replace
	r.setPause(10 * time.Millisecond)
	third := r.pausedUntil.Load()

	assert.Equal(t, second, third, "shorter pause should not replace longer one")
}

func TestRetry_SharedPause_ContextCancellation(t *testing.T) {
	r := newTestRetry(DefaultRetryOptions, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	// Set a long pause
	r.setPause(5 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after 50ms
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := r.waitForPause(ctx)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.True(t, elapsed < time.Second, "should have returned quickly after cancellation, elapsed: %v", elapsed)
}

func TestRetry_SharedPause_ExpiredPauseDoesNotBlock(t *testing.T) {
	r := newTestRetry(DefaultRetryOptions, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	// Set a pause in the past
	r.pausedUntil.Store(time.Now().Add(-time.Second).UnixNano())

	start := time.Now()
	err := r.waitForPause(context.Background())
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.True(t, elapsed < 10*time.Millisecond, "expired pause should not block, elapsed: %v", elapsed)
}

func TestRetry_SharedPause_429SetsPauseForOtherCalls(t *testing.T) {
	opts := RetryOptions{
		MaxAttempts: 1,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
		UseJitter:   false,
	}
	r := newTestRetry(opts, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	// Call Do with a 429 error — it will fail but should set the shared pause
	_ = r.Do(context.Background(), func() error {
		return make429Error(50) // 50ms
	})

	// Verify pause was set
	deadline := r.pausedUntil.Load()
	assert.True(t, deadline > 0, "pause should be set after 429")

	remaining := time.Until(time.Unix(0, deadline))
	assert.True(t, remaining > 0, "pause deadline should be in the future")
}

func TestRetry_SharedPause_ConcurrentSetPause(t *testing.T) {
	r := newTestRetry(DefaultRetryOptions, CircuitBreakerOptions{Enabled: false}, RateLimiterOptions{Enabled: false})

	var wg sync.WaitGroup
	var maxDeadline atomic.Int64

	// 10 goroutines set pauses concurrently with different durations
	for i := 0; i < 10; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := time.Duration(i*50+50) * time.Millisecond
			deadline := time.Now().Add(time.Duration(float64(d) * 1.1)).UnixNano()

			// Track the max deadline we expect
			for {
				cur := maxDeadline.Load()
				if deadline <= cur {
					break
				}
				if maxDeadline.CompareAndSwap(cur, deadline) {
					break
				}
			}

			r.setPause(d)
		}()
	}

	wg.Wait()

	// The stored pause should be close to the maximum deadline
	stored := r.pausedUntil.Load()
	assert.True(t, stored > 0, "pause should be set")

	// Allow some timing tolerance
	diff := time.Duration(maxDeadline.Load()-stored) * time.Nanosecond
	if diff < 0 {
		diff = -diff
	}
	assert.True(t, diff < 100*time.Millisecond,
		"stored pause should be close to max deadline, diff: %v", diff)
}
