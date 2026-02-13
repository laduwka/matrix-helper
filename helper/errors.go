package helper

import "fmt"

func Wrap(err error, msg string) error {
	return fmt.Errorf("%s: %w", msg, err)
}
