package rules

import "github.com/zubairbinshaukat/devpit/internal/scan"

// Caches returns the package manager caches, which live at known addresses
// rather than being found by walking. Looking them up is a stat; hunting for
// them would be a scan of the whole user profile.
//
// The tiers are about what emptying one costs. A download cache is Safe: the
// worst case is a slower install. A resolved dependency store is Review:
// restoring it needs a working network and an unchanged registry. pnpm's
// content-addressable store is Careful, because every node_modules on the
// machine is hard-linked into it and removing it quietly breaks all of them.
func Caches() []scan.Rule {
	return []scan.Rule{
		{
			Name:        "npm cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%LOCALAPPDATA%\npm-cache\_cacache`, `%APPDATA%\npm-cache\_cacache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; npm refills the cache as you install packages.",
			Description: "npm's download cache.",
		},
		{
			Name:        "yarn cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%LOCALAPPDATA%\Yarn\Cache`, `%LOCALAPPDATA%\Yarn\Berry\cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; yarn refills the cache on the next install.",
			Description: "Yarn's download cache.",
		},
		{
			Name:      "pnpm store",
			Kind:      scan.KindPackageCache,
			Locations: []string{`%LOCALAPPDATA%\pnpm\store`, `%USERPROFILE%\.pnpm-store`},
			// Every pnpm node_modules on this machine is hard-linked into
			// this store. Removing it leaves those folders full of dangling
			// links, so it never goes without a typed confirmation.
			Tier:        scan.TierCareful,
			RestoreHint: "Run pnpm install in each project; every package is downloaded again.",
			Description: "pnpm's shared package store, hard-linked into every project.",
		},
		{
			Name:        "pip and uv caches",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%LOCALAPPDATA%\pip\Cache`, `%LOCALAPPDATA%\uv\cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; pip downloads wheels again when it needs them.",
			Description: "pip and uv wheel caches.",
		},
		{
			Name:        "nuget http cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%LOCALAPPDATA%\NuGet\v3-cache`, `%LOCALAPPDATA%\NuGet\plugins-cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; NuGet re-downloads what it needs on the next restore.",
			Description: "NuGet's HTTP cache.",
		},
		{
			Name:        "nuget packages",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%USERPROFILE%\.nuget\packages`},
			Tier:        scan.TierReview,
			RestoreHint: "Run dotnet restore in each solution to download the packages again.",
			Description: "Extracted NuGet packages shared by every .NET project.",
		},
		{
			Name:        "gradle cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%USERPROFILE%\.gradle\caches`, `%USERPROFILE%\.gradle\wrapper\dists`},
			Tier:        scan.TierReview,
			RestoreHint: "Run gradlew build; Gradle downloads the dependencies and wrappers again.",
			Description: "Gradle's dependency cache and downloaded distributions.",
		},
		{
			Name:        "maven repository",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%USERPROFILE%\.m2\repository`},
			Tier:        scan.TierReview,
			RestoreHint: "Run mvn package; Maven downloads the dependencies again.",
			Description: "Maven's local repository.",
		},
		{
			Name:        "cargo registry",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%USERPROFILE%\.cargo\registry\cache`, `%USERPROFILE%\.cargo\registry\src`},
			Tier:        scan.TierReview,
			RestoreHint: "Run cargo build; Cargo downloads the crates again.",
			Description: "Downloaded and unpacked crates.",
		},
		{
			Name:        "go module cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%USERPROFILE%\go\pkg\mod\cache\download`},
			Tier:        scan.TierReview,
			RestoreHint: "Run go mod download, or just build; Go fetches the modules again.",
			Description: "Downloaded Go modules.",
		},
		{
			Name:        "go build cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%LOCALAPPDATA%\go-build`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; the next go build refills it, more slowly than usual.",
			Description: "Go's compiled object cache.",
		},
		{
			Name:        "composer cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%LOCALAPPDATA%\Composer`, `%APPDATA%\Composer\cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; Composer downloads packages again when it needs them.",
			Description: "PHP Composer's download cache.",
		},
		{
			Name:        "deno and bun caches",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%LOCALAPPDATA%\deno`, `%USERPROFILE%\.bun\install\cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; Deno and Bun re-fetch dependencies on the next run.",
			Description: "Deno and Bun dependency caches.",
		},
		{
			Name:        "electron cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%LOCALAPPDATA%\electron\Cache`, `%LOCALAPPDATA%\electron-builder\Cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; the next Electron build downloads what it needs.",
			Description: "Downloaded Electron runtimes and builder tools.",
		},
	}
}
