//go:build !windows

package shims

import "errors"

// errNoRegistry: the user PATH lives in the Windows registry.
var errNoRegistry = errors.New("the user PATH can only be changed on Windows")

type noPath struct{}

func (noPath) Read() (string, bool, bool, error) { return "", false, false, errNoRegistry }
func (noPath) Write(string, bool) error          { return errNoRegistry }

// UserPath has nothing to manage outside Windows.
func UserPath() PathStore { return noPath{} }

// BroadcastEnvironment does nothing outside Windows.
func BroadcastEnvironment() {}
