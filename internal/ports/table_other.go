//go:build !windows

package ports

import (
	"context"
	"errors"
)

// List is not implemented outside Windows; there is no equivalent of
// iphlpapi's owner-PID tables to enumerate against.
func List(ctx context.Context) ([]Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.ErrUnsupported
}
