//go:build !windows

package winapi

// CreateNewProcessGroup has no meaning outside Windows.
const CreateNewProcessGroup = 0

func sendCtrlBreak(int) error { return ErrUnsupported }
