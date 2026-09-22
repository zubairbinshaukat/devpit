package network

import (
	"context"
	"errors"
	"runtime"
)

// FlushDNS runs `ipconfig /flushdns` and returns its output. It is
// Windows-only: on any other OS it returns errors.ErrUnsupported without
// running anything.
func FlushDNS(ctx context.Context, opts Options) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.ErrUnsupported
	}
	return opts.runner().Run(ctx, nil, "ipconfig", "/flushdns")
}
