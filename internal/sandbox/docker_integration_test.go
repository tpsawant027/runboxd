//go:build integration

package sandbox

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

const testTimeout = 30 * time.Second

func newTestSandbox(t *testing.T) *DockerSandbox {
	t.Helper()
	registryPath := os.Getenv("REGISTRY_PATH")
	if registryPath == "" {
		registryPath = "../../language_registry.yml"
	}
	sb, err := NewDockerSandbox(registryPath)
	if err != nil {
		t.Skip("docker unavailable:", err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

func ensureRunResult(t *testing.T, got, want RunResult, wantStderrContains string) {
	t.Helper()
	if got.Status != want.Status {
		t.Errorf("status = %q, want %q", got.Status, want.Status)
	}
	if got.Stdout != want.Stdout {
		t.Errorf("stdout = %q, want %q", got.Stdout, want.Stdout)
	}
	if got.ExitCode != want.ExitCode {
		t.Errorf("exit code = %d, want %d", got.ExitCode, want.ExitCode)
	}
	if wantStderrContains != "" {
		if !strings.Contains(got.Stderr, wantStderrContains) {
			t.Errorf("stderr = %q, want to contain %q", got.Stderr, wantStderrContains)
		}
	} else if got.Stderr != want.Stderr {
		t.Errorf("stderr = %q, want %q", got.Stderr, want.Stderr)
	}
}

func TestRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := newTestSandbox(t)

	cases := []struct {
		name          string
		runSpec       RunSpec
		wantRunResult RunResult
	}{
		{
			name: "python",
			runSpec: RunSpec{
				Language: "python",
				Code:     "print(1+1)\n",
			},
			wantRunResult: RunResult{
				Status:   StatusOK,
				Stdout:   "2\n",
				ExitCode: 0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sb.Run(ctx, tc.runSpec)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			ensureRunResult(t, got, tc.wantRunResult, "")
		})
	}
}

func TestRunStdin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := newTestSandbox(t)

	cases := []struct {
		name          string
		runSpec       RunSpec
		wantRunResult RunResult
	}{
		{
			name: "python",
			runSpec: RunSpec{
				Language: "python",
				Code:     "print(input())\n",
				Stdin:    "hello\n",
			},
			wantRunResult: RunResult{
				Status:   StatusOK,
				Stdout:   "hello\n",
				ExitCode: 0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sb.Run(ctx, tc.runSpec)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			ensureRunResult(t, got, tc.wantRunResult, "")
		})
	}
}

func TestRunRuntimeError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := newTestSandbox(t)

	cases := []struct {
		name               string
		runSpec            RunSpec
		wantRunResult      RunResult
		wantStderrContains string
	}{
		{
			name: "python",
			runSpec: RunSpec{
				Language: "python",
				Code:     "raise Exception('oops')\n",
			},
			wantRunResult: RunResult{
				Status:   StatusRuntimeError,
				Stdout:   "",
				ExitCode: 1,
			},
			wantStderrContains: "Exception: oops",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sb.Run(ctx, tc.runSpec)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			ensureRunResult(t, got, tc.wantRunResult, tc.wantStderrContains)
		})
	}
}

func TestRunTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := newTestSandbox(t)

	cases := []struct {
		name          string
		runSpec       RunSpec
		wantRunResult RunResult
	}{
		{
			name: "python",
			runSpec: RunSpec{
				Language: "python",
				Code:     "import time; time.sleep(60)\n",
				Timeout:  5 * time.Second,
			},
			wantRunResult: RunResult{
				Status:   StatusTimeout,
				ExitCode: -1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sb.Run(ctx, tc.runSpec)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			ensureRunResult(t, got, tc.wantRunResult, "")
		})
	}
}

func TestRunOOM(t *testing.T) {
	// Longer ctx: OOM should trigger in <1s but we need headroom over RunSpec.Timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sb := newTestSandbox(t)

	// OOM kill requires Docker swap limit support. Without it Docker ignores
	// --memory-swap and the container spills to the swap partition instead of
	// being killed. Ask the daemon directly rather than guessing from cgroup paths.
	daemonInfo, err := sb.client.Info(ctx, client.InfoOptions{})
	if err != nil || !daemonInfo.Info.SwapLimit {
		t.Skip("docker swap limits not supported on this system; skipping OOM test")
	}

	cases := []struct {
		name          string
		runSpec       RunSpec
		wantRunResult RunResult
	}{
		{
			name: "python",
			runSpec: RunSpec{
				Language:    "python",
				MemoryBytes: 32 << 20, // 32 MiB
				Timeout:     30 * time.Second,
				Code:        "import os; os.urandom(2 << 30)\n",
			},
			wantRunResult: RunResult{
				Status:   StatusOOM,
				ExitCode: -1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sb.Run(ctx, tc.runSpec)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			ensureRunResult(t, got, tc.wantRunResult, "")
		})
	}
}

func TestRunFilesystem(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := newTestSandbox(t)

	cases := []struct {
		name          string
		runSpec       RunSpec
		wantRunResult RunResult
	}{
		{
			name: "write_and_read_in_workspace",
			runSpec: RunSpec{
				Language: "python",
				Code: `
with open("out.txt", "w") as f:
    f.write("hello")
with open("out.txt") as f:
    print(f.read())
`,
			},
			wantRunResult: RunResult{
				Status:   StatusOK,
				Stdout:   "hello\n",
				ExitCode: 0,
			},
		},
		{
			name: "write_outside_sandbox_denied",
			runSpec: RunSpec{
				Language: "python",
				Code: `
try:
    open("/oops.txt", "w")
    print("FAIL")
except (PermissionError, OSError):
    print("ok")
`,
			},
			wantRunResult: RunResult{
				Status:   StatusOK,
				Stdout:   "ok\n",
				ExitCode: 0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sb.Run(ctx, tc.runSpec)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			ensureRunResult(t, got, tc.wantRunResult, "")
		})
	}
}

func TestRunConcurrency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := newTestSandbox(t)

	const numRuns = 5

	var wg sync.WaitGroup
	for n := range numRuns {
		wg.Go(func() {
			got, err := sb.Run(ctx, RunSpec{
				Language: "python",
				Code:     fmt.Sprintf("print('run %d')\n", n),
			})
			if err != nil {
				t.Errorf("goroutine %d: Run: %v", n, err)
				return
			}
			if got.Status != StatusOK {
				t.Errorf("goroutine %d: status = %q, want %q", n, got.Status, StatusOK)
			}
			if got.Stdout != fmt.Sprintf("run %d\n", n) {
				t.Errorf("goroutine %d: stdout = %q, want %q", n, got.Stdout, fmt.Sprintf("Run %d\n", n))
			}
		})
	}
	wg.Wait()
}
