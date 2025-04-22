package helper

import (
	"math/rand"
	"strings"
	"time"

	"github.com/matrix-org/gomatrix"
)

const (
	closedKeyword = "close"
	doneKeyword   = "done"
)

func GetLimitTimestamp(days int) int64 {
	return time.Now().AddDate(0, 0, -days).Unix() * 1000
}

func ShouldRetry(err error) bool {
	if httpErr, ok := err.(gomatrix.HTTPError); ok {
		return httpErr.Code == 429 || httpErr.Code >= 500
	}
	if err.Error() == "http2: server sent GOAWAY and closed the connection; LastStreamID=1, ErrCode=ENHANCE_YOUR_CALM, debug=\"\"" {
		return true
	}
	return false
}

func IsRateLimited(err error) bool {
	if apiErr, ok := err.(*gomatrix.HTTPError); ok {
		return apiErr.Code == 429
	}
	if apiErr, ok := err.(gomatrix.HTTPError); ok {
		return apiErr.Code == 429
	}
	if err.Error() == "http2: server sent GOAWAY and closed the connection; LastStreamID=1, ErrCode=ENHANCE_YOUR_CALM, debug=\"\"" {
		return true
	}
	return false
}

func RandomInt(min, max int) int {
	return rand.Intn(max-min+1) + min
}

func HasCloseStatusMessage(roomName string) bool {
	name := strings.ToLower(roomName)
	return strings.Contains(name, closedKeyword) ||
		strings.Contains(name, doneKeyword)
}
