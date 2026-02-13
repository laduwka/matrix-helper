package helper

import (
	"testing"
	"time"

	"github.com/matrix-org/gomatrix"
	"github.com/stretchr/testify/assert"
)

func TestGetLimitTimestamp(t *testing.T) {
	t.Run("returns timestamp N days ago in milliseconds", func(t *testing.T) {
		days := 30
		before := time.Now().AddDate(0, 0, -days).Unix() * 1000
		result := GetLimitTimestamp(days)
		after := time.Now().AddDate(0, 0, -days).Unix() * 1000

		assert.GreaterOrEqual(t, result, before)
		assert.LessOrEqual(t, result, after)
	})

	t.Run("zero days returns current timestamp", func(t *testing.T) {
		now := time.Now().Unix() * 1000
		result := GetLimitTimestamp(0)
		assert.InDelta(t, now, result, 1000)
	})
}

func TestHasCloseStatusMessage(t *testing.T) {
	tests := []struct {
		name     string
		roomName string
		expected bool
	}{
		{"contains close", "Ticket close", true},
		{"contains closed", "Ticket closed", true},
		{"contains done", "Task done", true},
		{"contains CLOSE uppercase", "CLOSE THIS", true},
		{"contains Done mixed case", "Done with task", true},
		{"contains resolved", "Issue resolved", true},
		{"contains completed", "Task completed", true},
		{"contains RESOLVED uppercase", "RESOLVED", true},
		{"no keywords", "General Discussion", false},
		{"empty name", "", false},
		{"partial match close", "disclosure", false},
		{"contains close as substring", "we will close this", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasCloseStatusMessage(tt.roomName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			"429 rate limited",
			gomatrix.HTTPError{Code: 429, Message: "rate limited"},
			true,
		},
		{
			"500 server error",
			gomatrix.HTTPError{Code: 500, Message: "internal error"},
			true,
		},
		{
			"502 bad gateway",
			gomatrix.HTTPError{Code: 502, Message: "bad gateway"},
			true,
		},
		{
			"403 forbidden",
			gomatrix.HTTPError{Code: 403, Message: "forbidden"},
			false,
		},
		{
			"404 not found",
			gomatrix.HTTPError{Code: 404, Message: "not found"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldRetry(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			"429 value type",
			gomatrix.HTTPError{Code: 429, Message: "rate limited"},
			true,
		},
		{
			"429 pointer type",
			&gomatrix.HTTPError{Code: 429, Message: "rate limited"},
			true,
		},
		{
			"500 not rate limited",
			gomatrix.HTTPError{Code: 500, Message: "internal error"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRateLimited(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRandomInt(t *testing.T) {
	for i := 0; i < 100; i++ {
		result := RandomInt(5, 10)
		assert.GreaterOrEqual(t, result, 5)
		assert.LessOrEqual(t, result, 10)
	}
}
