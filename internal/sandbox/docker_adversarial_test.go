//go:build adversarial

package sandbox

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func runHostile(t *testing.T, sb *DockerSandbox, code string) RunResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	got, err := sb.Run(ctx, RunSpec{
		Language: "python",
		Code:     code,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run returned internal error: %v", err)
	}
	return got
}

func TestAdvNetworkBlocked(t *testing.T) {
	sb := newTestSandbox(t)
	code := `
import socket
socket.create_connection(("1.1.1.1",53),2)
`
	got := runHostile(t, sb, code)
	if got.Status != StatusRuntimeError {
		t.Errorf("got status %v, want StatusRuntimeError", got.Status)
	}
	if !strings.Contains(string(got.Stderr), "OSError") {
		t.Errorf("stderr = %q, want contains OSError", got.Stdout)
	}
	if !strings.Contains(string(got.Stderr), "Network is unreachable") {
		t.Errorf("stderr = %q, want contains 'Network is unreachable'", got.Stdout)
	}
}

func TestAdvForkBomb(t *testing.T) {
	sb := newTestSandbox(t)

	spec, err := sb.LangSpec("python", "")
	if err != nil {
		t.Fatalf("LangSpec: %v", err)
	}
	limit := spec.Limits.MaxPids + 10

	code := fmt.Sprintf(`
import os, sys, time
hit = False
for _ in range(%d):
    try:
        pid = os.fork()
    except BlockingIOError:   # EAGAIN: PidsLimit reached
        hit = True
        break
    if pid == 0:              # child: go inert, do NOT loop
        time.sleep(10)
        os._exit(0)
print("hit" if hit else "nolimit")
sys.exit(0 if hit else 1)
  `, limit)

	got := runHostile(t, sb, code)
	if !strings.Contains(got.Stdout, "hit") {
		t.Errorf("PidsLimit not enforced: stdout=%q status=%q", got.Stdout, got.Status)
	}

	// invariant: host/daemon unharmed - a normal run still works afterward
	ok := runHostile(t, sb, `print("ok")`)
	if ok.Status != StatusOK || ok.Stdout != "ok\n" {
		t.Errorf("sandbox unhealthy after fork bomb: status=%q stdout=%q", ok.Status, ok.Stdout)
	}
}

func TestAdvNoExec(t *testing.T) {
	sb := newTestSandbox(t)
	code := `
import os
with open("hello.sh", "w") as f:
    f.write("#!/bin/sh\necho hello\n")
os.chmod("hello.sh", 0o755)
os.execv("./hello.sh", ["hello.sh"])
`
	got := runHostile(t, sb, code)
	if got.Status != StatusRuntimeError {
		t.Errorf("got status %v, want StatusRuntimeError", got.Status)
	}
	if !strings.Contains(string(got.Stderr), "PermissionError") {
		t.Errorf("stderr = %q, want contains PermissionError", got.Stdout)
	}
}

func TestAdvReadonlyRootfs(t *testing.T) {
	sb := newTestSandbox(t)
	code := `
with open("/etc/script.sh", "w") as f:
    f.write("#!/bin/sh\necho hello\n")
`
	got := runHostile(t, sb, code)
	if got.Status != StatusRuntimeError {
		t.Errorf("got status %v, want StatusRuntimeError", got.Status)
	}
	if !strings.Contains(string(got.Stderr), "OSError") {
		t.Errorf("stderr = %q, want contains OSError", got.Stdout)
	}
	if !strings.Contains(string(got.Stderr), "Read-only file system") {
		t.Errorf("stderr = %q, want contains 'Read-only file system'", got.Stdout)
	}
}

func TestAdvCapsDropped(t *testing.T) {
	sb := newTestSandbox(t)
	code := `
import socket
socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_ICMP)  # raw socket requires CAP_NET_RAW
`
	got := runHostile(t, sb, code)
	if got.Status != StatusRuntimeError {
		t.Errorf("got status %v, want StatusRuntimeError", got.Status)
	}
	if !strings.Contains(string(got.Stderr), "PermissionError") {
		t.Errorf("stderr = %q, want contains PermissionError", got.Stdout)
	}
}

func TestAdvNoNewPrivileges(t *testing.T) {
	sb := newTestSandbox(t)
	code := `
with open("/proc/self/status") as f:
    print(f.read())
`
	got := runHostile(t, sb, code)
	if got.Status != StatusOK {
		t.Errorf("got status %v, want StatusOK", got.Status)
	}
	if !strings.Contains(string(got.Stdout), "NoNewPrivs:\t1") {
		t.Errorf("stdout = %q, want contains 'NoNewPrivs:\\t1'", got.Stdout)
	}
}

func TestAdvOutputFlood(t *testing.T) {
	sb := newTestSandbox(t)
	code := `
buf = b"x" * (8 * 1024 * 1024)  # 8 MiB
print(buf.decode())
`
	got := runHostile(t, sb, code)
	if got.Status != StatusOK {
		t.Errorf("got status %v, want StatusRuntimeError", got.Status)
	}
	if int64(len(got.Stdout)) > MaxOutputBytes {
		t.Errorf("stdout = %d bytes, want <= %d", len(got.Stdout), MaxOutputBytes)
	}
}

func TestAdvDiskFill(t *testing.T) {
	sb := newTestSandbox(t)
	code := `
buf = b"x" * (1024 * 1024)  		# 1 MiB, allocated once
with open("/sandbox/big", "wb") as f:
    for _ in range(100):		# attempt 100 MiB
        f.write(buf)
`
	got := runHostile(t, sb, code)
	if got.Status != StatusRuntimeError {
		t.Errorf("got status %v, want StatusRuntimeError", got.Status)
	}
	if !strings.Contains(string(got.Stderr), "OSError") {
		t.Errorf("stderr = %q, want contains OSError", got.Stdout)
	}
	if !strings.Contains(string(got.Stderr), "No space left on device") {
		t.Errorf("stderr = %q, want contains 'No space left on device'", got.Stdout)
	}
}

func TestAdvNoDockerSocket(t *testing.T) {
	sb := newTestSandbox(t)
	code := "import os;print(os.path.exists('/var/run/docker.sock'))"
	got := runHostile(t, sb, code)
	if got.Status != StatusOK {
		t.Errorf("got status %v, want StatusOK", got.Status)
	}
	if got.Stdout != "False\n" {
		t.Errorf("stdout = %q, want 'False\\n'", got.Stdout)
	}
}
