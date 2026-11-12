package extract

// Argv (rule 83). These three functions are the only places a hatchzip
// command line is built. They use only the words check, extract and restore
// and the flags -o, -y and --keep-archive: never -p or --password (the
// password goes through the child's environment, rule 79), never -q (check
// would print nothing to parse), never --replace or --skip-existing, never a
// shell. Every path is absolute ([checkRequest] refuses anything else before
// a run), so none can be read as a flag.

// checkArgs is `hatchzip check`.
func checkArgs(r Request) []string {
	a := []string{"check", r.Set.First, "-o", r.OutDir}
	if r.Mode == ModeKeepArchive {
		a = append(a, "--keep-archive")
	}
	return a
}

// extractArgs is `hatchzip extract`. It is the only argv with -y, and only
// Engine.Start calls it, which needs a ticket only a passed check makes
// (rule 76).
func extractArgs(r Request) []string {
	a := []string{"extract", r.Set.First, "-o", r.OutDir, "-y"}
	if r.Mode == ModeKeepArchive {
		a = append(a, "--keep-archive")
	}
	return a
}

// restoreArgs is `hatchzip restore`. Spike X9: no -o is needed, the journal
// knows the folder the extraction was started with.
func restoreArgs(r Request) []string { return []string{"restore", r.Set.First} }
