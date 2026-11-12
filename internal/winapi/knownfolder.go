package winapi

// DownloadsFolder is the user's Downloads folder (FOLDERID_Downloads), which
// may have been moved away from %USERPROFILE%\Downloads. Other platforms
// return [ErrUnsupported].
func DownloadsFolder() (string, error) { return downloadsFolder() }
