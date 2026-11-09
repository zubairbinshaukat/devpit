package sysinfo

import (
	"bytes"
	"encoding/json"
)

// reliabilityScript is the admin read the elevated worker runs: the drives'
// own wear, hours-on and read-error counters. Temperature is left out on
// purpose. It is a constant; the worker accepts no script from the TUI.
const reliabilityScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$out = @(Get-PhysicalDisk | ForEach-Object {
  $c = $_ | Get-StorageReliabilityCounter
  @{ disk = [int]$_.DeviceId; wear = $(if ($null -ne $c.Wear) { [int]$c.Wear } else { -1 }); hours = $(if ($null -ne $c.PowerOnHours) { [long]$c.PowerOnHours } else { -1 }); read_errors = $(if ($null -ne $c.ReadErrorsUncorrected) { [long]$c.ReadErrorsUncorrected } else { -1 }) }
})
ConvertTo-Json -Compress -Depth 3 -InputObject $out
`

// maxReliabilityBytes caps what the worker sends back.
const maxReliabilityBytes = 64 << 10

// parseReliability reads the admin read's output: a list, or one bare
// object, or nothing. Values out of range become "not reported".
func parseReliability(out []byte) ([]Reliability, error) {
	i := bytes.IndexAny(out, "[{")
	if i < 0 {
		return nil, errNoBatchOutput
	}
	var l list[Reliability]
	if err := json.Unmarshal(bytes.TrimSpace(out[i:]), &l); err != nil {
		return nil, err
	}
	rs := []Reliability(l)
	for k := range rs {
		if rs[k].WearPct < 0 || rs[k].WearPct > 100 {
			rs[k].WearPct = -1
		}
		if rs[k].PowerOnHours < 0 {
			rs[k].PowerOnHours = -1
		}
		if rs[k].ReadErrors < 0 {
			rs[k].ReadErrors = -1
		}
	}
	return rs, nil
}

// EncodeReliability is how the worker sends the counters: JSON, capped.
func EncodeReliability(rs []Reliability) (string, error) {
	if len(rs) > 64 {
		rs = rs[:64]
	}
	b, err := json.Marshal(rs)
	if err != nil {
		return "", err
	}
	if len(b) > maxReliabilityBytes {
		return "", errNoBatchOutput
	}
	return string(b), nil
}

// DecodeReliability reads what [EncodeReliability] wrote.
func DecodeReliability(s string) ([]Reliability, error) {
	if len(s) > maxReliabilityBytes {
		return nil, errNoBatchOutput
	}
	return parseReliability([]byte(s))
}

// MergeReliability puts counters on the drives they belong to.
func MergeReliability(di DiskInfo, rs []Reliability) DiskInfo {
	disks := append([]Disk(nil), di.Disks...)
	for i := range disks {
		for _, r := range rs {
			if r.Disk == disks[i].Number {
				rel := r
				disks[i].Rel = &rel
			}
		}
	}
	di.Disks = disks
	return di
}
