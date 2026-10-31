//go:build !linux

package sandbox

import "errors"

func platformProbe() error { return errors.New("not linux") }
