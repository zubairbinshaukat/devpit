//go:build !windows

package elevate

import "errors"

// jobTree has no Job Object outside Windows; killing the direct child is all
// there is.
type jobTree struct{}

func newJobTree() *jobTree { return &jobTree{} }

func (*jobTree) assign(int) error { return errors.ErrUnsupported }

func (*jobTree) kill() {}

func (*jobTree) release() {}
