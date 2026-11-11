package winapi

import (
	"errors"
	"strings"
)

// The English PDH paths of the per-share SMB byte counters. They are added
// with PdhAddEnglishCounterW, so they work on a Windows in any language: the
// localized names are never used.
//
// Both counters are PERF_COUNTER_BULK_COUNT ("RateOfCountsPerSecond64"): PDH
// formats them as bytes per second, but the raw value (FirstValue) is the
// running 64-bit count of bytes, which is what [ShareBytes.Read] returns.
// The instance is a wildcard; the share is picked out of the array by name.
const (
	// ClientShareReadBytesPath counts the bytes this PC has read from each
	// share it has open on another PC. Its instances are named like
	// `\192.168.1.178\Games-ab12` (one leading backslash, then host and
	// share as typed in the UNC path), plus `_Total`.
	ClientShareReadBytesPath = `\SMB Client Shares(*)\Read Bytes/sec`
	// ServerShareReadBytesPath counts the bytes other PCs have read from
	// each share this PC serves. Its instances are named like `\\*\Games-ab12`,
	// plus `_Total`.
	ServerShareReadBytesPath = `\SMB Server Shares(*)\Read Bytes/sec`
)

// ErrNoShareInstance means the SMB counter set opened, but has no instance for
// the share right now: the share is not open (receiver) or not served
// (sender) yet, or Windows dropped the instance with its session.
var ErrNoShareInstance = errors.New("winapi: the SMB counters have no instance for this share")

// OpenClientShareBytes opens the receiving side's byte counter for the share
// at unc (`\\host\share`): every byte this PC reads from that one share, by
// any program, and nothing else. Close it when done. It needs no
// administrator rights. Outside Windows it returns [ErrUnsupported].
func OpenClientShareBytes(unc string) (*ShareBytes, error) {
	want := clientInstanceKey(unc)
	if want == "" {
		return nil, errors.New("winapi: no share path")
	}
	return openShareBytes(ClientShareReadBytesPath, func(inst string) bool { return clientInstanceKey(inst) == want })
}

// OpenServerShareBytes opens the sending side's byte counter for the share
// this PC serves under the name share: every byte any PC reads from it, and
// nothing else. Close it when done. It needs no administrator rights.
// Outside Windows it returns [ErrUnsupported].
func OpenServerShareBytes(share string) (*ShareBytes, error) {
	want := serverInstanceKey(share)
	if want == "" {
		return nil, errors.New("winapi: no share name")
	}
	return openShareBytes(ServerShareReadBytesPath, func(inst string) bool { return serverInstanceKey(inst) == want })
}

// clientInstanceKey reduces a UNC path or an SMB Client Shares instance name
// to the form both share: no leading backslashes, lower case. Share and host
// names are case-insensitive in SMB, and PDH reports the instance in the case
// it was first opened with.
func clientInstanceKey(s string) string {
	s = strings.TrimRight(strings.TrimLeft(strings.TrimSpace(s), `\`), `\`)
	if s == "" || strings.EqualFold(s, "_Total") {
		return ""
	}
	return strings.ToLower(s)
}

// serverInstanceKey reduces a share name or an SMB Server Shares instance name
// (`\\*\name`, or `\\scope\name` for a scoped share) to the lower-case share
// name alone.
func serverInstanceKey(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), `\`)
	if i := strings.LastIndexByte(s, '\\'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" || strings.EqualFold(s, "_Total") {
		return ""
	}
	return strings.ToLower(s)
}

// byteTally turns a share instance's raw count into a count that only goes
// up. The instance belongs to the SMB session: when the session drops and
// comes back, Windows makes a new instance that counts from zero again, so a
// reading below the last one means a new instance, whose whole count is new.
type byteTally struct {
	started     bool
	last, total uint64
}

// add takes one raw reading and returns the total so far. The first reading
// is taken as it is: the bytes Windows counted for the instance before.
func (t *byteTally) add(raw uint64) uint64 {
	switch {
	case !t.started:
		t.started = true
		t.total = raw
	case raw >= t.last:
		t.total += raw - t.last
	default:
		t.total += raw
	}
	t.last = raw
	return t.total
}
