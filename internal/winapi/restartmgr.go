package winapi

// LockHolder names a process that holds an open handle to a file, as reported
// by the Windows Restart Manager. Devpit only ever shows these names; it never
// terminates a holder.
type LockHolder struct {
	// PID is the operating system process identifier.
	PID uint32
	// Name is the friendly application name Restart Manager reports, which is
	// usually the executable's description or file name, for example
	// "Code.exe" or "Visual Studio Code".
	Name string
}
