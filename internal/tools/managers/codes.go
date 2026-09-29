package managers

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file is the one place that turns an exit code into words. Every
// mapping is a row in [codeTable]; [Explain] only looks rows up, and
// [Codes] lists them so the documentation can be generated from the same
// table the program uses, never written by hand beside it.
//
// A row is only here with evidence. The sources, all Microsoft's own:
//
//   - winget: doc/windows/package-manager/winget/returnCodes.md in
//     microsoft/winget-cli (the 0x8A15xxxx family);
//   - MSIX and Store apps: "Troubleshooting packaging, deployment, and query
//     of Windows apps" (desktop-src/appxpkg/troubleshooting.md in
//     MicrosoftDocs/win32), the 0x80073Cxx and 0x80073Dxx family;
//   - Windows Installer: "MsiExec.exe and InstMsi.exe error messages"
//     (desktop-src/Msi/error-codes.md in MicrosoftDocs/win32), plain numbers
//     such as 1603;
//   - crashes: the NTSTATUS values in ntstatus.h, which Windows uses as the
//     exit code of a process it had to stop, and 0xE06D7363, the code every
//     exception thrown by the Microsoft C++ runtime carries.

// Family names the kind of source a code comes from. It groups the rows in
// the generated documentation and decides where [Explain] looks a code up.
type Family string

// The families of code [codeTable] holds.
const (
	// FamilyCrash is a Windows status code a process exits with when
	// Windows stopped it (an access violation, a corrupted heap).
	FamilyCrash Family = "Crash"
	// FamilyWinget is a winget return code, 0x8A15xxxx.
	FamilyWinget Family = "winget"
	// FamilyMSIX is a Windows app package (MSIX, Store app) deployment
	// error, 0x80073Cxx or 0x80073Dxx.
	FamilyMSIX Family = "MSIX and Store apps"
	// FamilyInstaller is a Windows Installer (MSI) exit code, a plain
	// number such as 1603.
	FamilyInstaller Family = "Windows Installer"
)

// Placeholders a row's text may hold. [Explain] fills {hex} with the code and
// {app} with "it"; [ExplainApp] fills {app} with the app's name.
const (
	placeholderApp = "{app}"
	placeholderHex = "{hex}"
)

// codeRow is one code and everything Devpit says about it.
type codeRow struct {
	code   uint32
	family Family
	// symbol is the name the source documents the code under.
	symbol string
	kind   VerdictKind
	// label is the short text on the row: lowercase, no full stop.
	label string
	// meaning says in plain words what happened.
	meaning string
	// next says in plain words what to do about it.
	next string
	// managers limits the row to these managers' exit codes. Empty means
	// any manager. Windows Installer codes are small numbers that mean
	// nothing as a winget or npm exit code, so they name choco here and
	// reach winget only through its "exit code: N" output line.
	managers []string
}

// nextTryAgain and the other shared next steps keep the wording of the same
// advice identical wherever it appears.
const (
	nextTryAgain    = "Try again in a few minutes."
	nextCrash       = "Try again. If it crashes again, restart your PC and try once more. If it still crashes, install this app by hand."
	nextFreeSpace   = "Free some space on drive C, then try again."
	nextPolicy      = "Your PC's settings block this. Ask the person who manages it."
	nextRestartTry  = "Restart your PC, then try again."
	nextAdminByHand = "Open Windows Terminal as administrator and run the update again."
)

// codeTable is every mapped code. Order is the order the documentation lists
// them in: crashes, winget, MSIX, Windows Installer, each by code.
var codeTable = []codeRow{
	// ---- Crashes (ntstatus.h) ----
	{
		code: 0xC0000005, family: FamilyCrash, symbol: "STATUS_ACCESS_VIOLATION", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program used memory it was not allowed to use, and Windows stopped it.",
		next:    nextCrash,
	},
	{
		code: 0xC0000006, family: FamilyCrash, symbol: "STATUS_IN_PAGE_ERROR", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "Windows could not read part of the program from disk, and the program stopped.",
		next:    "Try again. If it repeats, check that the disk and any network drive are working.",
	},
	{
		code: 0xC000001D, family: FamilyCrash, symbol: "STATUS_ILLEGAL_INSTRUCTION", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program tried to run a CPU instruction that this PC does not have.",
		next:    "This version may not be made for your PC. Look for another version of the app.",
	},
	{
		code: 0xC00000FD, family: FamilyCrash, symbol: "STATUS_STACK_OVERFLOW", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program used up its working space and Windows stopped it.",
		next:    nextCrash,
	},
	{
		code: 0xC0000374, family: FamilyCrash, symbol: "STATUS_HEAP_CORRUPTION", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program damaged its own memory, and Windows stopped it.",
		next:    nextCrash,
	},
	{
		code: 0xC0000409, family: FamilyCrash, symbol: "STATUS_STACK_BUFFER_OVERRUN", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program hit a fatal error and stopped itself. Windows uses this code for a program that aborts on purpose, too.",
		next:    nextCrash,
	},
	{
		code: 0xC0000417, family: FamilyCrash, symbol: "STATUS_INVALID_CRUNTIME_PARAMETER", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program passed a bad value to Windows' C library, and the library stopped it.",
		next:    nextCrash,
	},
	{
		code: 0xC0000420, family: FamilyCrash, symbol: "STATUS_ASSERTION_FAILURE", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program checked its own state, found it wrong, and stopped.",
		next:    nextCrash,
	},
	{
		code: 0xC0000602, family: FamilyCrash, symbol: "STATUS_FAIL_FAST_EXCEPTION", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program hit a fatal error and asked Windows to stop it at once.",
		next:    nextCrash,
	},
	{
		code: 0xE06D7363, family: FamilyCrash, symbol: "Microsoft C++ exception", kind: VerdictCrashed,
		label:   "crashed ({hex})",
		meaning: "The program threw an error that nothing in it caught.",
		next:    nextCrash,
	},
	{
		code: 0xC0000017, family: FamilyCrash, symbol: "STATUS_NO_MEMORY", kind: VerdictFailed,
		label:   "ran out of memory ({hex})",
		meaning: "The program could not get the memory it asked for.",
		next:    "Close other apps, then try again.",
	},
	{
		code: 0xC000012D, family: FamilyCrash, symbol: "STATUS_COMMITMENT_LIMIT", kind: VerdictFailed,
		label:   "ran out of memory ({hex})",
		meaning: "Windows had no memory left to give (RAM and the page file are full).",
		next:    "Close other apps, then try again.",
	},
	{
		code: 0xC000007B, family: FamilyCrash, symbol: "STATUS_INVALID_IMAGE_FORMAT", kind: VerdictFailed,
		label:   "could not start ({hex})",
		meaning: "The program, or a file it needs, is damaged or is the wrong kind for this PC.",
		next:    "Reinstall the app, then try again.",
	},
	{
		code: 0xC0000135, family: FamilyCrash, symbol: "STATUS_DLL_NOT_FOUND", kind: VerdictFailed,
		label:   "could not start ({hex})",
		meaning: "A file the program needs (a DLL) is missing.",
		next:    "Reinstall the app, then try again.",
	},
	{
		code: 0xC0000139, family: FamilyCrash, symbol: "STATUS_ENTRYPOINT_NOT_FOUND", kind: VerdictFailed,
		label:   "could not start ({hex})",
		meaning: "A file the program needs is there, but it is the wrong version.",
		next:    "Reinstall the app, then try again.",
	},
	{
		code: 0xC0000142, family: FamilyCrash, symbol: "STATUS_DLL_INIT_FAILED", kind: VerdictFailed,
		label:   "could not start ({hex})",
		meaning: "A file the program needs (a DLL) would not start.",
		next:    nextRestartTry,
	},
	{
		code: 0xC000013A, family: FamilyCrash, symbol: "STATUS_CONTROL_C_EXIT", kind: VerdictCancelled,
		label:   "stopped early ({hex})",
		meaning: "The program was ended with Ctrl+C, or its window was closed.",
		next:    "Run the update again and leave the window open.",
	},

	// ---- winget (returnCodes.md) ----
	{
		code: 0x8A150005, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_CTRL_SIGNAL_RECEIVED", kind: VerdictCancelled,
		label: textCancelled, meaning: "winget was stopped before it finished.",
		next: "Run the update again.",
	},
	{
		code: 0x8A15006A, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_APPTERMINATION_RECEIVED", kind: VerdictCancelled,
		label: textCancelled, meaning: "winget was told to shut down before it finished.",
		next: "Run the update again.",
	},
	{
		code: 0x8A150008, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_DOWNLOAD_FAILED", kind: VerdictFailed,
		label: "download failed", meaning: "winget could not download the installer.",
		next: "Check your internet connection, then try again.",
	},
	{
		code: 0x8A150086, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALLER_ZERO_BYTE_FILE", kind: VerdictFailed,
		label: "download was empty", meaning: "winget downloaded a file with nothing in it.",
		next: "Check your internet connection, then try again.",
	},
	{
		code: 0x8A150010, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_NO_APPLICABLE_INSTALLER", kind: VerdictFailed,
		label: "no installer for this machine", meaning: "None of the installers for this app fit this PC.",
		next: "This app may not support your PC or Windows version.",
	},
	{
		code: 0x8A150011, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALLER_HASH_MISMATCH", kind: VerdictFailed,
		label: "download failed its check", meaning: "The installer winget downloaded is not the file the catalog describes.",
		next: "Try again later. Do not run that installer by hand.",
	},
	{
		code: 0x8A150019, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_COMMAND_REQUIRES_ADMIN", kind: VerdictNeedsAdmin,
		label: "needs admin rights", meaning: "winget needs administrator rights for this.",
		next: nextAdminByHand,
	},
	{
		code: 0x8A15002B, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_UPDATE_NOT_APPLICABLE", kind: VerdictUpToDate,
		label: textUpToDate, meaning: "There is no newer version that fits this PC.",
		next: "Nothing to do.",
	},
	{
		code: 0x8A15002D, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALLER_SECURITY_CHECK_FAILED", kind: VerdictFailed,
		label: "installer failed a security check", meaning: "The installer did not pass winget's security check.",
		next: "Try again later. Do not run that installer by hand.",
	},
	{
		code: 0x8A15003A, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_BLOCKED_BY_POLICY", kind: VerdictFailed,
		label: "blocked by policy", meaning: "A Group Policy on this PC blocks this winget action.",
		next: nextPolicy,
	},
	{
		code: 0x8A150049, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_MSI_INSTALL_FAILED", kind: VerdictFailed,
		label: "msi install failed", meaning: "The app's MSI installer reported an error.",
		next: "Close the app if it is open, restart your PC, and try again.",
	},
	{
		code: 0x8A150050, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_UPGRADE_VERSION_UNKNOWN", kind: VerdictFailed,
		label: "installed version unknown, can't upgrade", meaning: "winget cannot tell which version is installed, so it will not update it.",
		next: "Update it by hand, or run: winget upgrade --include-unknown.",
	},
	{
		code: 0x8A150056, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALLER_PROHIBITS_ELEVATION", kind: VerdictFailed,
		label: "can't be updated as administrator", meaning: "This installer refuses to run with administrator rights.",
		next: "Run the update from a normal terminal, not an administrator one.",
	},
	{
		code: 0x8A150061, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_PACKAGE_ALREADY_INSTALLED", kind: VerdictUpToDate,
		label: textUpToDate, meaning: "A version of this app is already installed.",
		next: "Nothing to do.",
	},
	{
		code: 0x8A150068, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_PACKAGE_IS_PINNED", kind: VerdictPinned,
		label: textPinned, meaning: "The app has a pin that stops updates.",
		next: "To update it, remove the pin: winget pin remove --id <app id>.",
	},
	{
		code: 0x8A15006D, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_SERVICE_UNAVAILABLE", kind: VerdictFailed,
		label: "a Windows service is busy", meaning: "A service winget needs is busy or not available.",
		next: nextTryAgain,
	},
	{
		code: 0x8A15007D, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_ADMIN_CONTEXT_ACTION_PROHIBITED", kind: VerdictFailed,
		label: "can't be updated as administrator", meaning: "This app was installed for one user, and winget will not change it from an administrator window.",
		next: "Run the update from a normal terminal, not an administrator one.",
	},
	{
		code: 0x8A15008E, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_UPDATE_INSTALL_TECHNOLOGY_MISMATCH", kind: VerdictFailed,
		label: "update can't replace this install", meaning: "The new version is installed in a different way than the one on this PC.",
		next: "Uninstall the app in Settings, then install the new version.",
	},
	{
		code: 0x8A150101, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_PACKAGE_IN_USE", kind: VerdictInUse,
		label: textInUse, meaning: "The app is running, so its files cannot be replaced.",
		next: "Close {app}, including its tray icon, then run the update again.",
	},
	{
		code: 0x8A150102, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_INSTALL_IN_PROGRESS", kind: VerdictFailed,
		label: "another install is running", meaning: "Another installation is already in progress.",
		next: "Wait for the other install to finish, then try again.",
	},
	{
		code: 0x8A150103, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_FILE_IN_USE", kind: VerdictInUse,
		label: textInUse, meaning: "One or more of the app's files are in use.",
		next: "Close {app}, including its tray icon, then run the update again.",
	},
	{
		code: 0x8A150104, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_MISSING_DEPENDENCY", kind: VerdictFailed,
		label: "a needed component is missing", meaning: "This app needs something that is not on this PC.",
		next: "Install what it needs, then try again. winget lists it in the log (press l).",
	},
	{
		code: 0x8A150105, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_DISK_FULL", kind: VerdictFailed,
		label: "disk full", meaning: "There is no more space on the disk.",
		next: nextFreeSpace,
	},
	{
		code: 0x8A150106, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_INSUFFICIENT_MEMORY", kind: VerdictFailed,
		label: "not enough memory", meaning: "There is not enough memory to install.",
		next: "Close other apps, then try again.",
	},
	{
		code: 0x8A150107, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_NO_NETWORK", kind: VerdictFailed,
		label: "no network", meaning: "This app needs the internet to install.",
		next: "Connect to a network, then try again.",
	},
	{
		code: 0x8A150109, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_REBOOT_REQUIRED_TO_FINISH", kind: VerdictRestart,
		label: textRestart, meaning: "The app is updated. Windows must restart to finish.",
		next: "Restart your PC when it suits you.",
	},
	{
		code: 0x8A15010A, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_REBOOT_REQUIRED_FOR_INSTALL", kind: VerdictRestart,
		label: textRestart, meaning: "The update needs a restart.",
		next: "Restart your PC, then run the update again if the app is still old.",
	},
	{
		code: 0x8A15010B, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_REBOOT_INITIATED", kind: VerdictRestart,
		label: textRestart, meaning: "The installer started a restart of your PC.",
		next: "Restart your PC when it suits you.",
	},
	{
		code: 0x8A15010C, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_CANCELLED_BY_USER", kind: VerdictCancelled,
		label: textCancelled, meaning: "The installation was cancelled, often by saying no to its own admin prompt.",
		next: "Run the update again and accept the prompt.",
	},
	{
		code: 0x8A15010D, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_ALREADY_INSTALLED", kind: VerdictUpToDate,
		label: textUpToDate, meaning: "Another version of this app is already installed.",
		next: "Nothing to do.",
	},
	{
		code: 0x8A15010F, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_BLOCKED_BY_POLICY", kind: VerdictFailed,
		label: "blocked by policy", meaning: "Your organization's policies stop this install.",
		next: nextPolicy,
	},
	{
		code: 0x8A150111, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_PACKAGE_IN_USE_BY_APPLICATION", kind: VerdictInUse,
		label: textInUse, meaning: "Another program is using the app.",
		next: "Close {app} and any program that uses it, then run the update again.",
	},
	{
		code: 0x8A150113, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_SYSTEM_NOT_SUPPORTED", kind: VerdictFailed,
		label: "this PC is not supported", meaning: "This app does not support this PC.",
		next: "This app may need a newer Windows or a different kind of PC.",
	},
	{
		code: 0x8A150114, family: FamilyWinget, symbol: "APPINSTALLER_CLI_ERROR_INSTALL_UPGRADE_NOT_SUPPORTED", kind: VerdictFailed,
		label: "installer can't update in place", meaning: "The installer cannot update an app that is already installed.",
		next: "Uninstall the app in Settings, then install the new version.",
	},

	// ---- MSIX and Store apps (appxpkg troubleshooting) ----
	{
		code: 0x80073CF0, family: FamilyMSIX, symbol: "ERROR_INSTALL_OPEN_PACKAGE_FAILED", kind: VerdictFailed,
		label: "package could not be opened", meaning: "Windows could not open the app's package. It may be broken, unsigned, or blocked.",
		next: "Try again. If it repeats, the download may be broken.",
	},
	{
		code: 0x80073CF3, family: FamilyMSIX, symbol: "ERROR_INSTALL_RESOLVE_DEPENDENCY_FAILED", kind: VerdictFailed,
		label: "package conflict or missing part", meaning: "The package clashes with one already installed, needs something that is missing, or is not made for this PC.",
		next: "Install Windows updates, then try again. If it repeats, uninstall the app and install it again.",
	},
	{
		code: 0x80073CF4, family: FamilyMSIX, symbol: "ERROR_INSTALL_OUT_OF_DISK_SPACE", kind: VerdictFailed,
		label: "disk full", meaning: "There is not enough disk space.",
		next: nextFreeSpace,
	},
	{
		code: 0x80073CF6, family: FamilyMSIX, symbol: "ERROR_INSTALL_REGISTRATION_FAILURE", kind: VerdictFailed,
		label: "package could not be registered", meaning: "Windows could not register the app.",
		next: nextRestartTry,
	},
	{
		code: 0x80073CF9, family: FamilyMSIX, symbol: "ERROR_INSTALL_FAILED", kind: VerdictFailed,
		label: "package install failed", meaning: "Windows could not install the app package.",
		next: "Try again. If it repeats, tell the app's maker. Windows' AppXDeployment-Server event log has the details.",
	},
	{
		code: 0x80073CFB, family: FamilyMSIX, symbol: "ERROR_PACKAGE_ALREADY_EXISTS", kind: VerdictFailed,
		label: "same package already installed", meaning: "The package is already installed, and it is not identical to this one, so Windows will not replace it.",
		next: "Uninstall the app in Settings, then install it again.",
	},
	{
		code: 0x80073D01, family: FamilyMSIX, symbol: "ERROR_DEPLOYMENT_BLOCKED_BY_POLICY", kind: VerdictFailed,
		label: "blocked by policy", meaning: "A policy on this PC blocks installing this package.",
		next: nextPolicy,
	},
	{
		code: 0x80073D02, family: FamilyMSIX, symbol: "ERROR_PACKAGES_IN_USE", kind: VerdictInUse,
		label: textInUse, meaning: "The app is running, so Windows cannot replace what it uses.",
		next: "Close {app}, including its tray icon, then run the update again.",
	},
	{
		code: 0x80073D28, family: FamilyMSIX, symbol: "ERROR_PACKAGED_SERVICE_REQUIRES_ADMIN_PRIVILEGES", kind: VerdictNeedsAdmin,
		label: "needs admin rights", meaning: "This app installs a background service, and Windows only allows that for an administrator.",
		next: "Devpit retries it once at the end, with one admin prompt. To do it yourself: " + strings.ToLower(nextAdminByHand[:1]) + nextAdminByHand[1:],
	},

	// ---- Windows Installer (MsiExec error codes) ----
	{
		code: 1601, family: FamilyInstaller, symbol: "ERROR_INSTALL_SERVICE_FAILURE", kind: VerdictFailed, managers: []string{"choco"},
		label: "Windows Installer not available", meaning: "The Windows Installer service could not be reached.",
		next: nextRestartTry,
	},
	{
		code: 1602, family: FamilyInstaller, symbol: "ERROR_INSTALL_USEREXIT", kind: VerdictCancelled, managers: []string{"choco"},
		label: "installer was cancelled", meaning: "The installation was cancelled.",
		next: "Run the update again and accept any prompt.",
	},
	{
		code: 1603, family: FamilyInstaller, symbol: "ERROR_INSTALL_FAILURE", kind: VerdictFailed, managers: []string{"choco"},
		label: "installer failed (1603)", meaning: "The installer hit a fatal error. This code says little by itself: a locked file, a missing right and a half-finished earlier install can all cause it.",
		next: "Close the app if it is open, restart your PC, and try again.",
	},
	{
		code: 1618, family: FamilyInstaller, symbol: "ERROR_INSTALL_ALREADY_RUNNING", kind: VerdictFailed, managers: []string{"choco"},
		label: "another install is running", meaning: "Another installation is already in progress.",
		next: "Wait for the other install (or Windows Update) to finish, then try again.",
	},
	{
		code: 1619, family: FamilyInstaller, symbol: "ERROR_INSTALL_PACKAGE_OPEN_FAILED", kind: VerdictFailed, managers: []string{"choco"},
		label: "installer file could not be opened", meaning: "The installer file could not be opened.",
		next: "Try again. If it repeats, the download may be broken.",
	},
	{
		code: 1620, family: FamilyInstaller, symbol: "ERROR_INSTALL_PACKAGE_INVALID", kind: VerdictFailed, managers: []string{"choco"},
		label: "installer file is not valid", meaning: "The installer file is not a valid Windows Installer package.",
		next: "Try again. If it repeats, the download may be broken.",
	},
	{
		code: 1625, family: FamilyInstaller, symbol: "ERROR_INSTALL_PACKAGE_REJECTED", kind: VerdictFailed, managers: []string{"choco"},
		label: "blocked by policy", meaning: "System policy forbids this installation.",
		next: nextPolicy,
	},
	{
		code: 1632, family: FamilyInstaller, symbol: "ERROR_INSTALL_TEMP_UNWRITABLE", kind: VerdictFailed, managers: []string{"choco"},
		label: "Temp folder is full or locked", meaning: "The Temp folder is full or cannot be written to.",
		next: nextFreeSpace,
	},
	{
		code: 1633, family: FamilyInstaller, symbol: "ERROR_INSTALL_PLATFORM_UNSUPPORTED", kind: VerdictFailed, managers: []string{"choco"},
		label: "installer does not fit this PC", meaning: "The installer does not support this kind of PC.",
		next: "Look for a version made for your PC.",
	},
	{
		code: 1638, family: FamilyInstaller, symbol: "ERROR_PRODUCT_VERSION", kind: VerdictFailed, managers: []string{"choco"},
		label: "another version is in the way", meaning: "Another version of this app is already installed, and this installer cannot go on top of it.",
		next: "Uninstall the old version in Settings, then install the new one.",
	},
	{
		code: 1641, family: FamilyInstaller, symbol: "ERROR_SUCCESS_REBOOT_INITIATED", kind: VerdictRestart, managers: []string{"choco"},
		label: textRestart, meaning: "The installer worked and started a restart of your PC.",
		next: "Restart your PC when it suits you.",
	},
	{
		code: 3010, family: FamilyInstaller, symbol: "ERROR_SUCCESS_REBOOT_REQUIRED", kind: VerdictRestart, managers: []string{"choco"},
		label: textRestart, meaning: "The installer worked. Windows must restart to finish.",
		next: "Restart your PC when it suits you.",
	},
}

// codeIndex finds rows by code. It is built once, when the package loads,
// from [codeTable]; nothing changes it afterwards.
var codeIndex = buildIndex(codeTable)

// buildIndex maps each code to its rows, in table order.
func buildIndex(rows []codeRow) map[uint32][]int {
	idx := make(map[uint32][]int, len(rows))
	for i, r := range rows {
		idx[r.code] = append(idx[r.code], i)
	}
	return idx
}

// applies reports whether the row's code means this in manager's exit codes.
func (r codeRow) applies(manager string) bool {
	if len(r.managers) == 0 {
		return true
	}
	for _, m := range r.managers {
		if m == manager {
			return true
		}
	}
	return false
}

// lookupCode finds the row for a code in manager's exit codes.
func lookupCode(manager string, code uint32) (codeRow, bool) {
	for _, i := range codeIndex[code] {
		if codeTable[i].applies(manager) {
			return codeTable[i], true
		}
	}
	return codeRow{}, false
}

// hexOf spells a code the way the sources do: 0x8A150101, and 0xC0000409.
func hexOf(code uint32) string { return fmt.Sprintf("0x%08X", code) }

// fill puts the code and the app's name into a row's text.
func fill(s string, code uint32, app string) string {
	if app == "" {
		app = "it"
	}
	return strings.NewReplacer(placeholderHex, hexOf(code), placeholderApp, app).Replace(s)
}

// verdict is the row as a [Verdict] for one app.
func (r codeRow) verdict(app string) Verdict {
	return Verdict{Kind: r.kind, Text: fill(r.label, r.code, app), Next: fill(r.next, r.code, app), Code: r.code}
}

// CodeInfo is one row of the table, for listing. Its text is what a row on
// screen shows when the app's name is not known ("it" stands in for {app}).
type CodeInfo struct {
	// Code is the value as an unsigned 32-bit number.
	Code uint32
	// Hex is Code spelled as the sources spell it, "0x80073D28". Windows
	// Installer codes are plain numbers, and Hex is that number in decimal.
	Hex string
	// Family says where the code comes from.
	Family Family
	// Symbol is the name the source documents the code under.
	Symbol string
	// Label is the short text on the row.
	Label string
	// Meaning says in plain words what happened.
	Meaning string
	// NextStep says in plain words what to do about it.
	NextStep string
	// Kind is how Devpit counts it: updated, failed, needs admin, crashed.
	Kind VerdictKind
	// Managers is which managers' exit codes it applies to; empty means all.
	Managers []string
}

// Codes lists every code Devpit turns into words, in documentation order.
// The list is a copy: changing it changes nothing. It exists so the
// documentation is generated from the table the program runs on, and a test
// keeps the table honest (no duplicate rows, no empty text, every code
// spelled the way its source spells it).
func Codes() []CodeInfo {
	out := make([]CodeInfo, len(codeTable))
	for i, r := range codeTable {
		hex := hexOf(r.code)
		if r.family == FamilyInstaller {
			hex = strconv.FormatUint(uint64(r.code), 10)
		}
		out[i] = CodeInfo{
			Code: r.code, Hex: hex, Family: r.family, Symbol: r.symbol,
			Label: fill(r.label, r.code, ""), Meaning: r.meaning, NextStep: fill(r.next, r.code, ""),
			Kind: r.kind, Managers: append([]string(nil), r.managers...),
		}
	}
	return out
}

// installerExitPattern finds the exit code winget prints after an installer
// it ran fails: "Installer failed with exit code: 1603", or, for an installer
// that crashed, the unsigned status "exit code: 3221226505" (a relayed one
// may be the signed "-1073740791"). The trailing \b keeps "exit code:
// 0x80073d28" out: that one is hex and [hexPattern] reads it. It is English
// text; under another display language the winget code alone is used.
var installerExitPattern = regexp.MustCompile(`(?i)exit code:?\s*(-?\d{1,10})\b`)

// hexPattern finds a code written as 0x followed by eight hex digits.
var hexPattern = regexp.MustCompile(`(?i)\b0x([0-9a-f]{8})\b`)

// parseExitCode reads a decimal exit code as the DWORD Windows keeps it: the
// unsigned value, or the signed int32 spelling of the same bits.
func parseExitCode(s string) (uint32, bool) {
	if strings.HasPrefix(s, "-") {
		n, err := strconv.ParseInt(s, 10, 32)
		return uint32(int32(n)), err == nil //nolint:gosec // deliberate wrap: the int32 and uint32 forms of an exit code are the same code.
	}
	n, err := strconv.ParseUint(s, 10, 32)
	return uint32(n), err == nil
}

// explainFromOutput looks for a code in winget's output when its own exit
// code was a general failure that hides the real one: the exit code the
// installer returned, or an MSIX error written out in hex.
func explainFromOutput(lines []string) (codeRow, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		if m := installerExitPattern.FindStringSubmatch(lines[i]); m != nil {
			if code, ok := parseExitCode(m[1]); ok {
				// The Windows Installer rows are keyed to choco; a crash
				// status means the same from any installer.
				if r, ok := lookupCode("choco", code); ok && (r.family == FamilyInstaller || r.family == FamilyCrash) {
					return r, true
				}
			}
		}
		if m := hexPattern.FindStringSubmatch(lines[i]); m != nil {
			if n, err := strconv.ParseUint(m[1], 16, 32); err == nil {
				if r, ok := lookupCode("winget", uint32(n)); ok && r.family != FamilyInstaller {
					return r, true
				}
			}
		}
	}
	return codeRow{}, false
}
