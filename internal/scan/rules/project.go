package rules

import "github.com/zubairbinshaukat/devpit/internal/scan"

// Markers shared by several rules, named so the table reads as English.
var (
	nodeMarkers   = []string{"package.json"}
	gradleMarkers = []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}
	dotnetMarkers = []string{"*.csproj", "*.fsproj", "*.vbproj", "*.vcxproj", "*.sln", "*.slnx"}
	pythonMarkers = []string{"pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "Pipfile"}
)

// nodeRules covers the JavaScript world, which produces more reclaimable
// bytes than every other ecosystem on a developer machine put together.
func nodeRules() []scan.Rule {
	return []scan.Rule{
		{
			Name:    "node_modules",
			Kind:    scan.KindProjectJunk,
			Names:   []string{"node_modules"},
			Markers: nodeMarkers,
			// A node_modules with no package.json beside it is still almost
			// certainly installed dependencies, so it is listed rather than
			// hidden, flagged unverified, and never pre-ticked. Deciding for
			// the user in either direction would be worse than showing them.
			AllowUnverified: true,
			Tier:            scan.TierSafe,
			RestoreHint:     "Run npm install, pnpm install or yarn in the project folder.",
			Description:     "Installed Node dependencies, rebuilt from the lockfile.",
		},
		{
			Name:        "dist",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"dist"},
			Markers:     nodeMarkers,
			Tier:        scan.TierReview,
			RestoreHint: "Run the project's build script, usually npm run build.",
			Description: "Bundled build output.",
		},
		{
			Name:        "next",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".next"},
			Markers:     nodeMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run next build or next dev; Next.js recreates it.",
			Description: "Next.js build and dev cache.",
		},
		{
			Name:        "nuxt",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".nuxt", ".output"},
			Markers:     nodeMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run nuxt build or nuxt dev; Nuxt recreates it.",
			Description: "Nuxt build output.",
		},
		{
			Name:        "svelte-kit",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".svelte-kit"},
			Markers:     nodeMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run npm run dev or npm run build; SvelteKit recreates it.",
			Description: "SvelteKit generated files.",
		},
		{
			Name:        "angular cache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".angular"},
			Markers:     []string{"angular.json"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run ng build or ng serve; the Angular CLI recreates it.",
			Description: "Angular CLI build cache.",
		},
		{
			Name:        "turbo cache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".turbo"},
			Markers:     []string{"turbo.json", "package.json"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run turbo build; the next build repopulates the cache.",
			Description: "Turborepo task cache.",
		},
		{
			Name:        "parcel cache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".parcel-cache"},
			Markers:     nodeMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run the Parcel build again; the first build is slower.",
			Description: "Parcel bundler cache.",
		},
		{
			Name:        "test coverage",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"coverage", ".nyc_output"},
			Markers:     nodeMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run the test suite with coverage enabled again.",
			Description: "Coverage reports from the last test run.",
		},
		{
			Name:        "expo cache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".expo", ".expo-shared"},
			Markers:     []string{"app.json", "app.config.js", "app.config.ts", "package.json"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run expo start; Expo recreates it.",
			Description: "Expo development cache.",
		},
		{
			Name:        "docusaurus cache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".docusaurus"},
			Markers:     []string{"docusaurus.config.js", "docusaurus.config.ts", "package.json"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run npm run build; Docusaurus recreates it.",
			Description: "Docusaurus build cache.",
		},
		{
			Name:        "astro cache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".astro"},
			Markers:     []string{"astro.config.mjs", "astro.config.ts", "astro.config.js", "package.json"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run astro build or astro dev; Astro recreates it.",
			Description: "Astro generated types and cache.",
		},
		{
			Name:        "serverless artifacts",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".serverless"},
			Markers:     []string{"serverless.yml", "serverless.yaml", "serverless.ts"},
			Tier:        scan.TierReview,
			RestoreHint: "Run serverless package or serverless deploy again.",
			Description: "Packaged Serverless Framework artifacts.",
		},
	}
}

// rustRules covers Cargo, whose target directory is routinely the largest
// single folder in a Rust project.
func rustRules() []scan.Rule {
	return []scan.Rule{
		{
			Name:        "cargo target",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"target"},
			Markers:     []string{"Cargo.toml"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run cargo build; the first build after this is slower.",
			Description: "Rust build output and incremental cache.",
		},
		{
			Name:        "sbt target",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"target"},
			Markers:     []string{"build.sbt"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run sbt compile; sbt rebuilds it.",
			Description: "sbt build output.",
		},
		{
			Name:        "zig cache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".zig-cache", "zig-cache", "zig-out"},
			Markers:     []string{"build.zig"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run zig build; Zig recreates it.",
			Description: "Zig build cache and output.",
		},
	}
}

// dotnetRules cover bin and obj, which only count as junk when a project or
// solution file proves the folder is a .NET project. Plenty of repositories
// have a hand-written bin directory full of scripts.
func dotnetRules() []scan.Rule {
	return []scan.Rule{
		{
			Name:        "dotnet bin",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"bin"},
			Markers:     dotnetMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run dotnet build or build the solution in Visual Studio.",
			Description: ".NET compiled output.",
		},
		{
			Name:        "dotnet obj",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"obj"},
			Markers:     dotnetMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run dotnet restore and dotnet build.",
			Description: ".NET intermediate build files.",
		},
	}
}

// pythonRules cover the interpreter's caches and virtual environments.
func pythonRules() []scan.Rule {
	return []scan.Rule{
		{
			Name:        "pycache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"__pycache__"},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; Python rewrites it the next time the module is imported.",
			Description: "Compiled Python bytecode.",
		},
		{
			Name:        "python tool caches",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", ".nox"},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; the tool rebuilds its cache on the next run.",
			Description: "pytest, mypy, ruff and tox caches.",
		},
		{
			Name:    "virtual environment",
			Kind:    scan.KindProjectJunk,
			Names:   []string{".venv", "venv", "virtualenv"},
			Markers: pythonMarkers,
			// Recreating an environment means re-downloading every wheel, and
			// an environment can hold packages installed by hand that no
			// requirements file records. Review, never Safe.
			Tier:        scan.TierReview,
			RestoreHint: "Recreate with python -m venv .venv and pip install -r requirements.txt.",
			Description: "Python virtual environment.",
		},
	}
}

// jvmRules cover Gradle and Maven build output inside a project. The
// user-level Gradle and Maven caches are package caches and live in caches.go.
func jvmRules() []scan.Rule {
	return []scan.Rule{
		{
			Name:        "gradle build",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"build"},
			Markers:     gradleMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run gradlew build; Gradle rebuilds it.",
			Description: "Gradle build output.",
		},
		{
			Name:        "gradle project cache",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".gradle"},
			Markers:     gradleMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Run gradlew build; the first build after this is slower.",
			Description: "Gradle's per-project cache.",
		},
		{
			Name:        "maven target",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"target"},
			Markers:     []string{"pom.xml"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run mvn package; Maven rebuilds it.",
			Description: "Maven build output.",
		},
		{
			Name:        "android cxx",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".cxx"},
			Markers:     gradleMarkers,
			Tier:        scan.TierSafe,
			RestoreHint: "Rebuild the Android project; Gradle regenerates the native build.",
			Description: "Android native build intermediates.",
		},
	}
}

// otherLanguageRules is the long tail: one entry per ecosystem, each gated on
// the file that proves the ecosystem is there.
func otherLanguageRules() []scan.Rule {
	return []scan.Rule{
		{
			Name:        "terraform",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".terraform"},
			Markers:     []string{"*.tf", "*.tf.json"},
			Tier:        scan.TierReview,
			RestoreHint: "Run terraform init to download the providers and modules again.",
			Description: "Downloaded Terraform providers and modules.",
		},
		{
			// Markers used to be ["ProjectSettings", "Assets"], an any-of:
			// either sibling folder alone was enough to call a Temp or
			// Library directory Unity junk. A folder named "Assets" is
			// common (a generic term, not a Unity one), so that turned any
			// build tool's plain "Temp" or "Library" output directory next
			// to an unrelated "Assets" folder into a false match. Unity's
			// own marker for "this directory is a Unity project root" is
			// ProjectSettings/ProjectVersion.txt, a file every Unity
			// project has and nothing else does; requiring it (rather than
			// the ProjectSettings folder alone) rules out any project that
			// merely happens to have a folder by that name too.
			Name:        "unity library",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"Library", "Temp", "Obj", "Logs"},
			Markers:     []string{"ProjectSettings/ProjectVersion.txt"},
			Tier:        scan.TierReview,
			RestoreHint: "Open the project in Unity; it reimports every asset, which takes a while.",
			Description: "Unity's import cache and build intermediates.",
		},
		{
			Name:        "flutter dart_tool",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".dart_tool"},
			Markers:     []string{"pubspec.yaml"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run flutter pub get, then flutter run or flutter build.",
			Description: "Dart and Flutter build cache.",
		},
		{
			Name:        "elm artifacts",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"elm-stuff"},
			Markers:     []string{"elm.json"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run elm make; Elm re-downloads and recompiles the packages.",
			Description: "Elm compiled packages.",
		},
		{
			Name:        "haskell stack work",
			Kind:        scan.KindProjectJunk,
			Names:       []string{".stack-work"},
			Markers:     []string{"stack.yaml", "package.yaml"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run stack build; the first build after this is slower.",
			Description: "Stack build output.",
		},
		{
			Name:        "erlang and elixir build",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"_build", "deps"},
			Markers:     []string{"mix.exs", "rebar.config", "dune-project"},
			Tier:        scan.TierSafe,
			RestoreHint: "Run mix deps.get and mix compile, or rebar3 compile.",
			Description: "Elixir and Erlang dependencies and build output.",
		},
		{
			Name:        "composer vendor",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"vendor"},
			Markers:     []string{"composer.json"},
			Tier:        scan.TierReview,
			RestoreHint: "Run composer install to fetch the packages again.",
			Description: "PHP Composer dependencies.",
		},
		{
			Name:        "cocoapods",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"Pods"},
			Markers:     []string{"Podfile"},
			Tier:        scan.TierReview,
			RestoreHint: "Run pod install to fetch the pods again.",
			Description: "CocoaPods dependencies.",
		},
		{
			Name:        "cmake build",
			Kind:        scan.KindProjectJunk,
			Names:       []string{"cmake-build-debug", "cmake-build-release", "cmake-build-relwithdebinfo"},
			Markers:     []string{"CMakeLists.txt"},
			Tier:        scan.TierSafe,
			RestoreHint: "Reconfigure and rebuild with cmake.",
			Description: "CMake build directory.",
		},
	}
}
