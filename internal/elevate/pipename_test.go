package elevate

import "testing"

func TestPipeServerPIDAcceptsOnlyWhatLaunchMakes(t *testing.T) {
	pid, err := pipeServerPID(pipeName(1234, "0123456789abcdef0123456789abcdef"))
	if err != nil || pid != 1234 {
		t.Fatalf("pipeServerPID = %d, %v; want 1234", pid, err)
	}
	for _, name := range []string{
		`\\evilhost\pipe\devpit-1234-abcdef01`, // a remote pipe
		`\\.\pipe\devpit-0-abcdef01`,           // no process has PID 0
		`\\.\pipe\devpit-1234-ABCDEF01`,        // Launch writes lowercase hex
		`\\.\pipe\devpit-test-1234-abcdef01`,
		`\\.\pipe\devpit-1234-abc`, // too little randomness
		`\\.\pipe\devpit-99999999999-abcdef01`,
		`\\.\pipe\devpit-1234-abcdef01\..\x`,
		"",
	} {
		if _, err := pipeServerPID(name); err == nil {
			t.Errorf("pipeServerPID(%q) accepted it", name)
		}
	}
}

func TestStartedBefore(t *testing.T) {
	if !startedBefore(100, 200) || !startedBefore(200, 200) {
		t.Error("the process that launched the worker was refused")
	}
	if startedBefore(300, 200) {
		t.Error("a process created after the worker was accepted as its launcher")
	}
}
