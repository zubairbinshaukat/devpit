package elevate

import "time"

// Options configures [Launch].
type Options struct {
	// ConnectTimeout bounds how long Launch waits for the worker to
	// connect to the pipe once the UAC prompt is accepted. Zero uses a
	// 30s default.
	ConnectTimeout time.Duration
	// HelloTimeout bounds how long Launch waits for the worker's hello
	// once connected. Zero uses a 10s default.
	HelloTimeout time.Duration
	// ExtraArgs are appended on the worker's command line after the
	// reserved --elevated-worker --pipe <name> flags. Tests use this to
	// start a distinguishable worker; production code leaves it nil.
	ExtraArgs []string
}
