package managers

import "sort"

// sortInstalled orders by Name so output built from a Go map (npm's JSON
// decodes to one) is deterministic for callers and tests.
func sortInstalled(list []Installed) {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
}

// sortOutdated orders by Name for the same reason as [sortInstalled].
func sortOutdated(list []Outdated) {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
}
