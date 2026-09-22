package clean

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// volumeFlag is the Docker argument Devpit refuses to pass, ever. Volumes hold
// database data; `docker system prune --volumes` is the single most expensive
// mistake this program could make (safety rule 13).
const volumeFlag = "--volumes"

// volumeFlags is every spelling of that argument. `-v` is its shorthand and
// `--all-volumes` its buildx cousin, and refusing only the long form would be
// a check that reads like rule 13 without enforcing it. The same list is what
// internal/scan/rules attaches to every Docker command as ForbiddenArgs; this
// one is the independent second check docs/safety.md's row 13 promises, so it
// is written out here rather than imported from the rule table it guards.
var volumeFlags = []string{volumeFlag, "-v", "--all-volumes"}

// forbiddenArgsCommand wraps a bare name and argument list in the same type
// the scanner uses, so the refusal runs through [scan.Command.Valid] rather
// than through a second, subtly different implementation of it.
func forbiddenArgsCommand(name string, args []string) scan.Command {
	return scan.Command{
		Name:          name,
		Exe:           name,
		Args:          args,
		ForbiddenArgs: volumeFlags,
		// Valid insists on a restore hint, because a step offered to a user
		// must say how to undo it. This wrapper is never offered to anyone: it
		// exists for the duration of one argument check.
		RestoreHint: "n/a: this is an argument check, not a step",
	}
}

// reclaimedPattern matches the lines the package managers print when they say
// how much they freed: Docker's "Total reclaimed space: 1.216GB", Scoop's
// "Reclaimed 340 MB", npm's "reclaimed 1.2 GiB".
var reclaimedPattern = regexp.MustCompile(
	`(?i)reclaimed(?:\s+space)?\s*[:=]?\s*([0-9]+(?:[.,][0-9]+)?)\s*(b|[kmgtp]i?b)\b`,
)

// byteUnits maps the suffixes the tools print to their multiplier. Docker
// prints decimal units with SI names, everything with an "i" is binary; both
// are common enough that guessing one would misreport the other.
var byteUnits = map[string]float64{
	"b":   1,
	"kb":  1000,
	"mb":  1000 * 1000,
	"gb":  1000 * 1000 * 1000,
	"tb":  1000 * 1000 * 1000 * 1000,
	"pb":  1000 * 1000 * 1000 * 1000 * 1000,
	"kib": 1024,
	"mib": 1024 * 1024,
	"gib": 1024 * 1024 * 1024,
	"tib": 1024 * 1024 * 1024 * 1024,
	"pib": 1024 * 1024 * 1024 * 1024 * 1024,
}

// RunCommand runs a tool's own cleanup command, captures its output and parses
// the amount it says it reclaimed. It is the path for Docker, Scoop, npm and
// pip caches, where the tool knows how to clean itself and Devpit should not
// be deleting files behind its back.
//
// It refuses outright, before starting anything, if the arguments name
// volumes in any spelling the tools accept: the whole of [volumeFlags] through
// [scan.Command.Valid], plus the literal `--volumes=...` form that an
// exact-match check cannot see.
//
// The output is returned even when the command fails, because the last lines
// of a failed prune are what a person needs to see.
func RunCommand(ctx context.Context, name string, args ...string) (output string, reclaimed uint64, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(name) == "" {
		return "", 0, fmt.Errorf("clean: %w", ErrEmptyPath)
	}
	if err := forbiddenArgsCommand(name, args).Valid(); err != nil {
		var forbidden *scan.ForbiddenArgError
		if errors.As(err, &forbidden) {
			return "", 0, fmt.Errorf("%w: %s %s", ErrVolumesRefused, name, strings.Join(args, " "))
		}
		return "", 0, fmt.Errorf("clean: %s: %w", name, err)
	}
	for _, a := range args {
		// Valid compares whole arguments, so `--volumes=true` reaches here
		// unscathed. This is the original literal check, kept for that reason.
		if a == volumeFlag || strings.HasPrefix(a, volumeFlag+"=") {
			return "", 0, fmt.Errorf("%w: %s %s", ErrVolumesRefused, name, strings.Join(args, " "))
		}
	}

	// #nosec G204 -- the command and arguments come from Devpit's own catalog
	// of cleanup steps, never from scanned paths or user text.
	cmd := exec.CommandContext(ctx, name, args...)
	raw, runErr := cmd.CombinedOutput()
	output = string(raw)
	reclaimed = ParseReclaimed(output)
	if runErr != nil {
		return output, reclaimed, fmt.Errorf("clean: %s %s: %w", name, strings.Join(args, " "), runErr)
	}
	return output, reclaimed, nil
}

// RunScanCommand runs one of the scanner's command-based cleanups. It is the
// entry point callers should use: the step carries its own ForbiddenArgs, and
// checking them is the rule-table half of safety rule 13, which [RunCommand]'s
// fixed [volumeFlags] list cannot do on a step's behalf. Both checks run, in
// that order, before anything is started.
func RunScanCommand(ctx context.Context, cmd scan.Command) (output string, reclaimed uint64, err error) {
	if err := cmd.Valid(); err != nil {
		var forbidden *scan.ForbiddenArgError
		if errors.As(err, &forbidden) {
			return "", 0, fmt.Errorf("%w: %s %s", ErrVolumesRefused, cmd.Exe, strings.Join(cmd.Args, " "))
		}
		return "", 0, fmt.Errorf("clean: %s: %w", cmd.Name, err)
	}
	return RunCommand(ctx, cmd.Exe, cmd.Args...)
}

// ParseReclaimed pulls the largest "reclaimed" figure out of a tool's output
// and returns it in bytes. It returns zero when the tool said nothing about
// how much it freed, which several of them do not.
func ParseReclaimed(output string) uint64 {
	var best uint64
	for _, m := range reclaimedPattern.FindAllStringSubmatch(output, -1) {
		n, err := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
		if err != nil {
			continue
		}
		mult, ok := byteUnits[strings.ToLower(m[2])]
		if !ok {
			continue
		}
		v := n * mult
		if v < 0 || v > math.MaxUint64 || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if b := uint64(v); b > best {
			best = b
		}
	}
	return best
}
