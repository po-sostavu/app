//go:build !linux

package deploy

import "errors"

func Trusted(string, bool) error       { return errors.New("server deployment requires Linux") }
func syncDirectory(string) error       { return nil }
func Lock(string) (func(), error)      { return nil, errors.New("server deployment requires Linux") }
func FreeSpace(string) (uint64, error) { return 0, errors.New("server deployment requires Linux") }
