//go:build !windows

package elevate

import (
	"context"
	"errors"
)

// Launch is not implemented outside Windows: there is no ShellExecuteExW
// and no UAC to elevate through.
func Launch(context.Context, string, Options) (*Client, error) {
	return nil, errors.ErrUnsupported
}
