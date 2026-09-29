package host

import "time"

// startSlack allows for the clock's granularity when comparing a process's
// creation time with the time its manifest says the share started.
const startSlack = 2 * time.Second

// sameOwner reports whether a process created at created can be the one that
// started a share at startedAt. The owner was running before it started the
// share; a process created after that got the PID once the owner had died.
func sameOwner(created, startedAt time.Time) bool {
	return !created.After(startedAt.Add(startSlack))
}
