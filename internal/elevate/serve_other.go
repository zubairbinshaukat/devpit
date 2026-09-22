//go:build !windows

package elevate

import (
	"context"
	"errors"
)

// Serve is not implemented outside Windows: there is no named pipe to dial.
func Serve(context.Context, string, Executor) error {
	return errors.ErrUnsupported
}
