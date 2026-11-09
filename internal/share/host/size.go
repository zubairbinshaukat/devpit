package host

import (
	"context"
	"io/fs"
	"os"
	"path"
)

// FolderSize is what [MeasureFolder] found in the shared folder.
type FolderSize struct {
	// Bytes is the total size of the regular files found.
	Bytes int64
	// Files is how many regular files that is.
	Files int64
	// Skipped counts what was left out: links and other entries that are not
	// plain files or folders (never sized, never entered), and folders that
	// could not be read. A non-zero Skipped means Bytes may be short.
	Skipped int
}

// MeasureFolder adds up the size of the files under root, once, so the share
// card can say how far along a transfer is. It only reads folder listings: it
// never opens a file, never follows a link, never writes, and needs no admin.
// It returns when the walk is finished, or with ctx's error and what it had
// counted so far.
func MeasureFolder(ctx context.Context, root string) (FolderSize, error) {
	return measureFS(ctx, os.DirFS(root), ".")
}

// measureFS is [MeasureFolder] over any file system, so the walk can be tested
// without a disk. The walk is iterative, with a stack, so a deep tree cannot
// overflow the goroutine's stack, and it checks ctx once per folder.
func measureFS(ctx context.Context, fsys fs.FS, root string) (FolderSize, error) {
	var size FolderSize
	stack := []string{root}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return size, err
		}
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			// A folder that cannot be listed is said so, and the walk goes on.
			size.Skipped++
			continue
		}
		for _, e := range entries {
			switch typ := e.Type(); {
			case typ&(fs.ModeSymlink|fs.ModeIrregular) != 0:
				// A symlink or a Windows junction or other reparse point: a
				// leaf, even if it also says it is a folder.
				size.Skipped++
			case typ.IsDir():
				stack = append(stack, path.Join(dir, e.Name()))
			case typ.IsRegular():
				info, err := e.Info()
				if err != nil {
					size.Skipped++
					continue
				}
				size.Bytes += info.Size()
				size.Files++
			default:
				// A device, a pipe or anything else that is not a plain file:
				// neither sized nor entered.
				size.Skipped++
			}
		}
	}
	return size, nil
}
