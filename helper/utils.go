package helper

import (
	"strings"
	"time"
)

var closeKeywords = []string{"close", "done", "resolved", "completed"}

func GetLimitTimestamp(days int) int64 {
	return time.Now().AddDate(0, 0, -days).Unix() * 1000
}

func HasCloseStatusMessage(roomName string) bool {
	name := strings.ToLower(roomName)
	for _, kw := range closeKeywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	return false
}
