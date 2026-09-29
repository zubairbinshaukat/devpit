package winapi

// KeepAwake asks Windows not to put the PC to sleep until the returned
// release function is called. The display may still turn off: a file copy
// needs the system awake, not the screen.
//
// release is never nil and is safe to call more than once, so a caller can
// defer it without checking err first. On a platform with no such call the
// error is [ErrUnsupported] and release does nothing.
func KeepAwake() (release func(), err error) { return keepAwake() }
