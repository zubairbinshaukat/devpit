//go:build !windows

package winapi

import "time"

func processStart(int) (time.Time, bool) { return time.Time{}, false }

func processImage(int) (string, error) { return "", ErrUnsupported }

func bootTime() (time.Time, error) { return time.Time{}, ErrUnsupported }
