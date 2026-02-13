package helper

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	sb := &smartBackOff{inner: inner, lastErr: &lastErr}

	delay := sb.NextBackOff()
	assert.Equal(t, 200*time.Millisecond, delay)
}

type mockBackOff struct {
	nextDelay time.Duration
}

func (m *mockBackOff) NextBackOff() time.Duration {
	return m.nextDelay
}

func (m *mockBackOff) Reset() {}
