package helper

import (
	"errors"
	"fmt"
)

var (
	ErrRateLimited  = errors.New("rate limited")
	ErrUnauthorized = errors.New("unauthorized")
	ErrNotFound     = errors.New("not found")
)

func Wrap(err error, msg string) error {
	return fmt.Errorf("%s: %w", msg, err)
}

func Is(err, target error) bool {
	return errors.Is(err, target)
}

func As(err error, target interface{}) bool {
	return errors.As(err, target)
}
