package rules

import "github.com/zubairbinshaukat/devpit/internal/scan"

// volumeFlags are the arguments that would make a docker prune destroy
// volumes. They are attached to every Docker command as ForbiddenArgs, and a
// test asserts that none of them ever appears in an Args list.
//
// This is safety rule 13, written as data rather than as a promise. Docker
// volumes hold database contents: the Postgres a developer has been filling
// for three months lives in one, and `docker system prune --volumes` is the
// single most destructive command in the whole tool's neighbourhood. Devpit
// never passes it, never offers it, and never lists volumes in Full Scan.
var volumeFlags = []string{"--volumes", "-v", "--all-volumes"}

// Docker returns the Docker cleanups, each one a call to Docker's own CLI.
//
// Docker's storage is a graph of layers shared between images, containers and
// build steps, and only the daemon knows which layers are still referenced.
// Deleting files under its data root by hand corrupts it. So these are
// commands, not rules: Devpit asks Docker to clean up and reads back what it
// reclaimed.
func Docker() []scan.Command {
	return []scan.Command{
		{
			Name:          "Docker build cache",
			Kind:          scan.KindDocker,
			Tier:          scan.TierSafe,
			Exe:           "docker",
			Args:          []string{"builder", "prune", "--force"},
			ForbiddenArgs: volumeFlags,
			Description:   "Layers left over from image builds.",
			RestoreHint:   "Nothing to restore; the next build re-creates what it needs, more slowly than usual.",
		},
		{
			Name:          "Docker dangling images",
			Kind:          scan.KindDocker,
			Tier:          scan.TierReview,
			Exe:           "docker",
			Args:          []string{"image", "prune", "--force"},
			ForbiddenArgs: volumeFlags,
			Description:   "Untagged images no container is using.",
			RestoreHint:   "Run docker pull or rebuild the image.",
		},
		{
			Name:          "Docker stopped containers",
			Kind:          scan.KindDocker,
			Tier:          scan.TierReview,
			Exe:           "docker",
			Args:          []string{"container", "prune", "--force"},
			ForbiddenArgs: volumeFlags,
			Description:   "Containers that have exited. Their volumes are untouched.",
			RestoreHint:   "Run docker run again. Anything the container wrote to a volume is still there.",
		},
	}
}
