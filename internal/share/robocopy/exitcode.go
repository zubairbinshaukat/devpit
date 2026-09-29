package robocopy

// Exit is robocopy's exit code, decoded. Robocopy does not return 0 for
// success: it returns a bitmask of what happened, and the bits never change
// with the language.
//
//	1   one or more files were copied
//	2   extra files or folders exist in the destination
//	4   mismatches were found (a file where a folder is, or the reverse)
//	8   some files or folders could not be copied
//	16  a serious error: nothing was copied
//
// Any value below 8 is a success, whatever combination of the low bits it
// holds. 8 and above means something failed. The table is in Microsoft's
// robocopy reference.
type Exit struct {
	// Code is the raw exit code.
	Code int
	// Copied is bit 1: something was copied.
	Copied bool
	// Extras is bit 2: the destination holds files the source does not.
	Extras bool
	// Mismatches is bit 4.
	Mismatches bool
	// SomeFailed is bit 8: at least one file could not be copied.
	SomeFailed bool
	// Fatal is bit 16: robocopy could not run properly at all.
	Fatal bool
}

// DecodeExit splits an exit code into its bits.
func DecodeExit(code int) Exit {
	return Exit{
		Code:       code,
		Copied:     code&1 != 0,
		Extras:     code&2 != 0,
		Mismatches: code&4 != 0,
		SomeFailed: code&8 != 0,
		Fatal:      code&16 != 0,
	}
}

// Success reports whether the copy finished with nothing failing: any code
// below 8.
func (e Exit) Success() bool { return e.Code >= 0 && e.Code < 8 }

// Describe says what the code means in easy English. It is the same whatever
// the Windows language, because it is built from the bits.
func (e Exit) Describe() string {
	switch {
	case e.Fatal:
		return "Robocopy hit a serious error and could not copy."
	case e.SomeFailed:
		return "Some files could not be copied."
	case e.Code == 0:
		return "Everything was already there. Nothing needed copying."
	case e.Mismatches:
		return "Done. Some items had a different type on each side."
	case e.Extras && e.Copied:
		return "Done. The destination also holds files that are not in the source."
	case e.Extras:
		return "Nothing new to copy. The destination holds extra files."
	default:
		return "All files were copied."
	}
}
