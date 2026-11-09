package sysinfo

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// batchScript is the one PowerShell run a read makes, fed on stdin. It is a
// constant: nothing is ever put into it. Every block has its own try/catch
// and writes its failure into "errors", so one blocked query never loses
// the rest. Lists are wrapped in @() because Windows PowerShell 5.1 turns a
// one-item list into a bare object, and enums are written as numbers so the
// output does not depend on the PC's language.
//
// The drives' wear counters need admin; unelevated the query costs 2.5 s to
// fail (docs/v0.5.0-specs-spikes.md), so the block runs only when elevated.
const batchScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$r = @{ errors = @{} }
try {
  $r.disks = @(Get-CimInstance -Namespace root/Microsoft/Windows/Storage -ClassName MSFT_PhysicalDisk | ForEach-Object {
    @{ id = [string]$_.DeviceId; name = [string]$_.FriendlyName; serial = [string]$_.SerialNumber; media = [int]$_.MediaType; bus = [int]$_.BusType; health = [int]$_.HealthStatus; size = [uint64]$_.Size }
  })
} catch { $r.errors.disks = $_.Exception.Message }
try {
  $r.parts = @(Get-CimInstance -Namespace root/Microsoft/Windows/Storage -ClassName MSFT_Partition | ForEach-Object {
    @{ disk = [int]$_.DiskNumber; letter = ([string]$_.DriveLetter).Trim([char]0); boot = [bool]$_.IsBoot }
  })
} catch { $r.errors.parts = $_.Exception.Message }
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if ($admin) {
  try {
    $r.rel = @(Get-PhysicalDisk | ForEach-Object {
      $c = $_ | Get-StorageReliabilityCounter
      @{ disk = [int]$_.DeviceId; wear = $(if ($null -ne $c.Wear) { [int]$c.Wear } else { -1 }); hours = $(if ($null -ne $c.PowerOnHours) { [long]$c.PowerOnHours } else { -1 }); read_errors = $(if ($null -ne $c.ReadErrorsUncorrected) { [long]$c.ReadErrorsUncorrected } else { -1 }) }
    })
  } catch { $r.errors.rel = $_.Exception.Message }
}
try {
  $sh = New-Object -ComObject Shell.Application
  $r.bitlocker = @(foreach ($d in [IO.DriveInfo]::GetDrives()) {
    if ($d.DriveType -eq [IO.DriveType]::Fixed -or $d.DriveType -eq [IO.DriveType]::Removable) {
      $v = $sh.NameSpace($d.Name).Self.ExtendedProperty('System.Volume.BitLockerProtection')
      @{ letter = $d.Name.Substring(0, 1); value = $(if ($null -ne $v) { [int]$v } else { -1 }) }
    }
  })
} catch { $r.errors.bitlocker = $_.Exception.Message }
try {
  $r.lic = @(Get-CimInstance -ClassName SoftwareLicensingProduct -Filter "PartialProductKey IS NOT NULL AND ApplicationID='55c92734-d682-4d71-983e-d6ec3f16059f'" | ForEach-Object {
    @{ status = [int]$_.LicenseStatus; channel = [string]$_.ProductKeyChannel }
  })
} catch { $r.errors.lic = $_.Exception.Message }
try {
  $r.hv = [bool](Get-CimInstance -ClassName Win32_ComputerSystem).HypervisorPresent
} catch { $r.errors.hv = $_.Exception.Message }
ConvertTo-Json -Compress -Depth 4 -InputObject $r
`

// list is a JSON list that also accepts a bare object or null, the shapes
// PowerShell 5.1 writes for one item or none.
type list[T any] []T

// UnmarshalJSON implements json.Unmarshaler.
func (l *list[T]) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || bytes.Equal(b, []byte("null")):
		*l = nil
		return nil
	case b[0] == '[':
		var s []T
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*l = s
		return nil
	}
	var one T
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*l = list[T]{one}
	return nil
}

// batch is the batch run's output.
type batch struct {
	Disks list[struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Serial string `json:"serial"`
		Media  int    `json:"media"`
		Bus    int    `json:"bus"`
		Health int    `json:"health"`
		Size   uint64 `json:"size"`
	}] `json:"disks"`
	Parts list[struct {
		Disk   int    `json:"disk"`
		Letter string `json:"letter"`
		Boot   bool   `json:"boot"`
	}] `json:"parts"`
	Rel       list[Reliability] `json:"rel"`
	BitLocker list[struct {
		Letter string `json:"letter"`
		Value  int    `json:"value"`
	}] `json:"bitlocker"`
	Lic list[struct {
		Status  int    `json:"status"`
		Channel string `json:"channel"`
	}] `json:"lic"`
	HV     *bool             `json:"hv"`
	Errors map[string]string `json:"errors"`
}

// errNoBatchOutput is a run that printed nothing usable.
var errNoBatchOutput = errors.New("PowerShell gave no answer")

// parseBatch reads the run's output. Anything before the JSON object (a
// profile banner, a warning) is skipped.
func parseBatch(out []byte) (batch, error) {
	var b batch
	i := bytes.IndexByte(out, '{')
	if i < 0 {
		return b, errNoBatchOutput
	}
	out = bytes.TrimPrefix(out[i:], []byte("\xef\xbb\xbf"))
	if err := json.Unmarshal(bytes.TrimSpace(out), &b); err != nil {
		return b, err
	}
	return b, nil
}

// busName maps MSFT_PhysicalDisk.BusType.
func busName(n int) string {
	switch n {
	case 17:
		return "NVMe"
	case 11:
		return "SATA"
	case 7:
		return "USB"
	case 3:
		return "ATA"
	case 8:
		return "RAID"
	case 10:
		return "SAS"
	case 12:
		return "SD card"
	case 13:
		return "MMC"
	case 15:
		return "virtual"
	case 16:
		return "Storage Spaces"
	case 18:
		return "SCM"
	case 19:
		return "UFS"
	}
	return ""
}

// diskInfo turns the batch into drives with their letters and BitLocker
// state. A failed block becomes the note, never a guess.
func (b batch) diskInfo() DiskInfo {
	var di DiskInfo
	for _, d := range b.Disks {
		disk := Disk{Model: strings.TrimSpace(d.Name), Serial: clean(d.Serial), SizeB: d.Size, Bus: busName(d.Bus)}
		disk.Number = atoiOr(d.ID, -1)
		switch d.Media {
		case 3:
			disk.Media = MediaHDD
		case 4:
			disk.Media = MediaSSD
		}
		switch d.Health {
		case 0:
			disk.Health = HealthGood
		case 1:
			disk.Health = HealthWarning
		case 2:
			disk.Health = HealthBad
		}
		for _, p := range b.Parts {
			if p.Disk != disk.Number {
				continue
			}
			if l := strings.Trim(p.Letter, "\x00 "); l != "" {
				disk.Letters = append(disk.Letters, strings.ToUpper(l))
			}
			disk.Boot = disk.Boot || p.Boot
		}
		for _, r := range b.Rel {
			if r.Disk == disk.Number {
				rel := r
				disk.Rel = &rel
			}
		}
		di.Disks = append(di.Disks, disk)
	}
	if len(b.BitLocker) > 0 {
		di.BitLocker = map[string]Tri{}
		for _, v := range b.BitLocker {
			di.BitLocker[strings.ToUpper(v.Letter)] = bitLockerOf(v.Value)
		}
	}
	switch {
	case b.Errors["disks"] != "":
		di.Note = "Windows did not list the drives, so their health is not reported."
	case b.Errors["bitlocker"] != "":
		di.Note = "BitLocker state is not reported."
	}
	return di
}

// bitLockerOf maps System.Volume.BitLockerProtection. 1 is on and 2 off;
// 6 is on and locked. Anything else is not known (the other values are
// not documented, see docs/v0.5.0-specs-spikes.md).
func bitLockerOf(v int) Tri {
	switch v {
	case 1, 6:
		return TriYes
	case 2:
		return TriNo
	}
	return TriUnknown
}

// licence returns whether Windows is activated and its channel, from the
// licence block.
func (b batch) licence() (Tri, string) {
	if b.Errors["lic"] != "" || len(b.Lic) == 0 {
		return TriUnknown, ""
	}
	l := b.Lic[0]
	return TriOf(l.Status == 1), strings.TrimSpace(l.Channel)
}

// atoiOr parses a small decimal number, or returns def.
func atoiOr(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
		if n > 1<<20 {
			return def
		}
	}
	return n
}
