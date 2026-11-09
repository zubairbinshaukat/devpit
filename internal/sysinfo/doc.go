// Package sysinfo reads what a PC is made of, for the Settings › PC specs
// page: the model, battery health, memory slots, drives and their health,
// screens, graphics, Windows and its licence, firmware and the network
// adapters.
//
// It only reads. Nothing here writes to the registry, the disk or the
// firmware, and the one PowerShell run it makes calls only the read cmdlets
// in its compile-time script (see TestScriptCallsOnlyReadCmdlets). It is an
// engine package: no Bubble Tea, no net or net/http, no cgo, no init and no
// package state other than lazily loaded DLL procedures.
//
// Missing data is a normal answer. A field this PC does not report stays at
// its zero value or [TriUnknown], and [Assess] never turns a missing value
// into a flag (TestMissingDataIsNeverAFlag).
//
// Privacy: serial numbers (system, board, disk, battery, memory, screen) and
// MAC addresses are kept on the [Report] so the screen can show them, but
// [Markdown] leaves them out unless asked, and the OEM product key in the
// firmware is never read: only whether the table is there.
package sysinfo
