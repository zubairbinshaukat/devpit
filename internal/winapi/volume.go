package winapi

// FileSystem returns the name of the file system of the volume that holds
// path: "NTFS", "exFAT", "FAT32", "ReFS". It is how Devpit knows before a
// copy whether the destination can hold a file over 4 GB.
func FileSystem(path string) (string, error) { return fileSystem(path) }
