// Package archive holds the naming rules of Devpit's "extract big archive in
// place" feature: which file names are archives hatchzip can extract, how the
// parts of a split set are named, which part is the first, what the output
// folder is called, and the names of the side files hatchzip keeps beside an
// unfinished extraction.
//
// It is a leaf package: it imports only the standard library and does no I/O,
// so Clean, the scan rules and the screens can import it cheaply.
package archive

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// Format is an archive format hatchzip extracts.
type Format string

// The supported formats (spike X7 confirms the exact list).
const (
	FormatZip Format = "zip"
	Format7z  Format = "7z"
	FormatRar Format = "rar"
)

// Set is one archive as hatchzip sees it: a single file or a split set.
type Set struct {
	Picked string   // the path the person chose (any part)
	First  string   // first volume, what hatchzip is given
	Parts  []string // every volume found beside it, in order (len 1 if single)
	Format Format
	Bytes  int64 // sum of Lstat sizes of Parts
}

var (
	// ErrNotArchive is a name that is not an archive Devpit can extract.
	ErrNotArchive = errors.New("not an archive Devpit can extract (.zip, .7z, .rar)")
	// ErrSplitZip is a native split ZIP part (.z01, .z02 …), which hatchzip
	// does not read.
	ErrSplitZip = errors.New("split ZIP (.z01) is not supported by hatchzip")
)

// Side files hatchzip owns. Devpit only ever Lstats them.
const (
	JournalSuffix = ".hatchzip" // <first>.hatchzip
	TailSuffix    = ".hatchzip-tail"
	FoldersSuffix = ".hatchzip-folders"
	PartSuffix    = ".hatchzip-part" // in the output folder
)

// naming is the parsed shape of one archive file name.
type naming struct {
	format Format
	// base is the name up to the archive extension ("x" for x.7z.003).
	base string
	// kind says how volumes are numbered.
	kind volumeKind
	// num is this file's volume number, 1-based.
	num int
	// width is the digit width of the volume number (byte splits and partN).
	width int
	// ext is the format extension as spelled in the name ("7z", "RAR").
	ext string
	// partWord is "part" as spelled ("part", "PART") for partN names.
	partWord string
}

type volumeKind int

const (
	single     volumeKind = iota // x.zip, x.7z, x.rar (x.rar may lead an .r00 set)
	byteSplit                    // x.7z.001, x.zip.002
	rarPartN                     // x.part1.rar, x.part01.rar
	rarOldPart                   // x.r00 (x.rar is volume 1, x.r00 volume 2)
)

func formatOf(ext string) (Format, bool) {
	switch strings.ToLower(ext) {
	case "zip":
		return FormatZip, true
	case "7z":
		return Format7z, true
	case "rar":
		return FormatRar, true
	}
	return "", false
}

// digits reports whether s is a non-empty run of ASCII digits.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// splitExt cuts name at its last dot: "a.b.c" -> "a.b", "c".
func splitExt(name string) (string, string, bool) {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || i == len(name)-1 {
		return name, "", false
	}
	return name[:i], name[i+1:], true
}

// parse reads a base file name (no directory).
func parse(name string) (naming, error) {
	stem, ext, ok := splitExt(name)
	if !ok {
		return naming{}, ErrNotArchive
	}
	lowExt := strings.ToLower(ext)

	// Native split ZIP: .z01, .z02 … (two or more digits after z).
	if len(lowExt) >= 3 && lowExt[0] == 'z' && digits(lowExt[1:]) {
		return naming{}, ErrSplitZip
	}

	// Byte split: <base>.<fmt>.<NNN>.
	if digits(ext) && len(ext) >= 3 {
		inner, fext, hasInner := splitExt(stem)
		if !hasInner {
			return naming{}, ErrNotArchive
		}
		f, known := formatOf(fext)
		if !known {
			return naming{}, ErrNotArchive
		}
		n, err := strconv.Atoi(ext)
		if err != nil || n < 1 {
			return naming{}, ErrNotArchive
		}
		return naming{format: f, base: inner, kind: byteSplit, num: n, width: len(ext), ext: fext}, nil
	}

	// Old-style RAR volume: .r00, .r01 … (x.rar is volume 1).
	if len(lowExt) >= 3 && lowExt[0] == 'r' && digits(lowExt[1:]) {
		n, err := strconv.Atoi(lowExt[1:])
		if err != nil {
			return naming{}, ErrNotArchive
		}
		rarExt := "rar"
		if ext[0] == 'R' {
			rarExt = "RAR"
		}
		return naming{format: FormatRar, base: stem, kind: rarOldPart, num: n + 2, width: len(ext) - 1, ext: rarExt}, nil
	}

	f, ok := formatOf(ext)
	if !ok {
		return naming{}, ErrNotArchive
	}
	if f == FormatRar {
		// New-style RAR volume: <base>.partN.rar.
		if inner, pext, ok := splitExt(stem); ok {
			low := strings.ToLower(pext)
			if strings.HasPrefix(low, "part") && digits(low[4:]) {
				n, err := strconv.Atoi(low[4:])
				if err == nil && n >= 1 {
					return naming{
						format: f, base: inner, kind: rarPartN, num: n,
						width: len(low) - 4, ext: ext, partWord: pext[:4],
					}, nil
				}
			}
		}
	}
	return naming{format: f, base: stem, kind: single, num: 1, ext: ext}, nil
}

func pad(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// volume is the file name of volume n (1-based) of the set nm belongs to.
func (nm naming) volume(n int) string {
	switch nm.kind {
	case byteSplit:
		return nm.base + "." + nm.ext + "." + pad(n, nm.width)
	case rarPartN:
		return nm.base + "." + nm.partWord + pad(n, nm.width) + "." + nm.ext
	case rarOldPart:
		if n == 1 {
			return nm.base + "." + nm.ext
		}
		r := "r"
		if nm.ext == "RAR" {
			r = "R"
		}
		return nm.base + "." + r + pad(n-2, nm.width)
	default:
		return nm.base + "." + nm.ext
	}
}

// Classify reads a file name (a base name or a path; only the base name is
// looked at). For a supported archive or volume it returns the format, the
// first volume's name (same directory) and ok. Otherwise ok is false and err
// is [ErrSplitZip] or [ErrNotArchive]. Name rules only: no I/O.
func Classify(name string) (f Format, first string, ok bool, err error) {
	dir, base := splitDir(name)
	nm, err := parse(base)
	if err != nil {
		return "", "", false, err
	}
	return nm.format, dir + nm.volume(1), true, nil
}

// IsSupported reports whether a file name is a supported archive or volume.
func IsSupported(name string) bool {
	_, _, ok, _ := Classify(name)
	return ok
}

// FirstVolume maps any part to the first: x.7z.003 -> x.7z.001,
// x.part3.rar -> x.part1.rar (keeping the digit width), x.r05 -> x.rar.
// A name that is not a supported archive is returned unchanged.
func FirstVolume(path string) string {
	_, first, ok, _ := Classify(path)
	if !ok {
		return path
	}
	return first
}

// Volume is the path of volume n (1-based) of the set path belongs to:
// Volume("x.7z.001", 3) is x.7z.003, Volume("x.rar", 2) is x.r00. For a
// single-file name only n == 1 is a volume, except x.rar, which may lead an
// old-style .r00 set. ok is false for a name that is not an archive or n < 1.
func Volume(path string, n int) (string, bool) {
	dir, base := splitDir(path)
	nm, err := parse(base)
	if err != nil || n < 1 {
		return "", false
	}
	if nm.kind == single {
		if n == 1 {
			return dir + nm.volume(1), true
		}
		if nm.format != FormatRar {
			return "", false
		}
		nm.kind, nm.width = rarOldPart, 2
	}
	return dir + nm.volume(n), true
}

// Stem is the output folder name: x.zip -> x, x.7z.001 -> x, x.part01.rar -> x.
// It works on a path too and returns only the name. A name that is not an
// archive loses only its last extension.
func Stem(first string) string {
	_, base := splitDir(first)
	nm, err := parse(base)
	if err != nil {
		s, _, _ := splitExt(base)
		return s
	}
	return nm.base
}

// JournalPath is the path of hatchzip's journal for the set whose first
// volume is first.
func JournalPath(first string) string { return first + JournalSuffix }

// IsSideFile reports a name ending in any of the four side-file suffixes.
func IsSideFile(name string) bool {
	low := strings.ToLower(name)
	for _, s := range []string{JournalSuffix, TailSuffix, FoldersSuffix, PartSuffix} {
		if strings.HasSuffix(low, s) {
			return true
		}
	}
	return false
}

// splitDir splits at the last slash or backslash, whatever the OS, so
// Windows-style paths behave the same in tests on Linux. dir keeps its
// trailing separator.
func splitDir(path string) (dir, base string) {
	i := strings.LastIndexAny(path, `\/`)
	if vol := filepath.VolumeName(path); i < 0 && vol != "" {
		return path[:len(vol)], path[len(vol):]
	}
	return path[:i+1], path[i+1:]
}
