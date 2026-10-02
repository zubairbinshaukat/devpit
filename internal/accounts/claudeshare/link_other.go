//go:build !windows

package claudeshare

import (
	"errors"
	"os"
)

// errNoJunctions: links are NTFS junctions, a Windows thing. Elsewhere the
// package plans copies, which is enough for the pure-logic tests.
var errNoJunctions = errors.New("links are made only on Windows")

func fsPath(p string) string { return p }

func isReparse(fi os.FileInfo) bool {
	return fi != nil && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
}

func readLink(p string) (linkInfo, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return linkInfo{}, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		if isReparse(fi) {
			return linkInfo{kind: linkOther}, nil
		}
		return linkInfo{}, nil
	}
	t, err := os.Readlink(p)
	if err != nil {
		return linkInfo{kind: linkSymlink}, err
	}
	return linkInfo{kind: linkSymlink, target: t}, nil
}

func createJunction(string, string) error { return errNoJunctions }

// removeLink removes a symbolic link only (there are no junctions here),
// and only when want is empty: Devpit never made it.
func removeLink(p, want string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !isReparse(fi) {
		return &NotALinkError{Path: p}
	}
	if want != "" {
		return errNoJunctions
	}
	return os.Remove(p)
}

func removeEmptyDir(p string) error { return os.Remove(p) }

func renameNoReplace(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: os.ErrExist}
	}
	return os.Rename(from, to)
}

func replaceFile(from, to string) error { return os.Rename(from, to) }

func inUse(error) bool { return false }

func volumeOf(string) (volInfo, error) { return volInfo{}, errNoJunctions }
