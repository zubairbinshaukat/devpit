//go:build !windows

package winapi

import "context"

// The join calls are not implemented outside Windows.

func wifiScan(string) ([]WifiNetwork, error)                   { return nil, ErrUnsupported }
func wifiVisible(string, string) (bool, error)                 { return false, ErrUnsupported }
func wifiHasProfile(string, string) (bool, error)              { return false, ErrUnsupported }
func wifiJoin(context.Context, string, string, string) error   { return ErrUnsupported }
func wifiConnectProfile(context.Context, string, string) error { return ErrUnsupported }
func wifiLeave(context.Context, string, string, string) error  { return ErrUnsupported }
func hasWiredGateway() (bool, error)                           { return false, ErrUnsupported }
