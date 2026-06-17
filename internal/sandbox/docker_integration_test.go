//go:build integration

package sandbox

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/tpsawant027/runboxd/internal/config"
)

func TestRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := NewTestSandboxFromEnv(t)

	cases := []struct {
		name          string
		runSpec       RunSpec
		wantRunResult RunResult
	}{
		{
			name: "python",
			runSpec: RunSpec{
				Language: "python",
				Code:     "print('hello from python')",
			},
			wantRunResult: RunResult{
				Status:   StatusOK,
				Stdout:   "hello from python\n",
				ExitCode: 0,
			},
		},
		{
			name: "nodejs",
			runSpec: RunSpec{
				Language: "nodejs",
				Code:     "console.log('hello from nodejs');",
			},
			wantRunResult: RunResult{
				Status:   StatusOK,
				Stdout:   "hello from nodejs\n",
				ExitCode: 0,
			},
		},
		{
			name: "c",
			runSpec: RunSpec{
				Language: "c",
				Code: `
#include <stdio.h>
int main(){ 
	printf("hello from c\n");
	return 0;
}`,
			},
			wantRunResult: RunResult{
				Status:   StatusOK,
				Stdout:   "hello from c\n",
				ExitCode: 0,
			},
		},
		{
			name: "java",
			runSpec: RunSpec{
				Language: "java",
				Code: `
public class Main {
	public static void main(String[] args) {
		System.out.println("hello from java");
	}
}`,
			},
			wantRunResult: RunResult{
				Status:   StatusOK,
				Stdout:   "hello from java\n",
				ExitCode: 0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SkipUnsupportedOnNsjail(t, tc.runSpec.Language)
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
	sb := NewTestSandboxFromEnv(t)

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
	sb := NewTestSandboxFromEnv(t)

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
		{
			name: "nodejs",
			runSpec: RunSpec{
				Language: "nodejs",
				Code:     "throw new Error('oops');\n",
			},
			wantRunResult: RunResult{
				Status:   StatusRuntimeError,
				Stdout:   "",
				ExitCode: 1,
			},
			wantStderrContains: "Error: oops",
		},
		{
			name: "c",
			runSpec: RunSpec{
				Language: "c",
				Code: `
#include <stdio.h>
int main(){
	int *ptr = NULL;
	*ptr = 42; // segfault
	printf("FAIL: should have segfaulted\n");
	return 0;
}`,
			},
			wantRunResult: RunResult{
				Status:   StatusRuntimeError,
				Stdout:   "",
				ExitCode: 139, // 128 + 11 (SIGSEGV)
			},
			wantStderrContains: "",
		},
		{
			name: "java",
			runSpec: RunSpec{
				Language: "java",
				Code: `
public class Main {
	public static void main(String[] args) {
		throw new RuntimeException("oops");
	}
}`,
			},
			wantRunResult: RunResult{
				Status:   StatusRuntimeError,
				Stdout:   "",
				ExitCode: 1,
			},
			wantStderrContains: "RuntimeException: oops",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SkipUnsupportedOnNsjail(t, tc.runSpec.Language)
			got, err := sb.Run(ctx, tc.runSpec)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			ensureRunResult(t, got, tc.wantRunResult, tc.wantStderrContains)
		})
	}
}

func TestRunCompileError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := NewTestSandboxFromEnv(t)

	cases := []struct {
		name               string
		runSpec            RunSpec
		wantRunResult      RunResult
		wantStderrContains string
	}{
		{
			name: "c",
			runSpec: RunSpec{
				Language: "c",
				Code: `
int main() {
	return 
}`,
			},
			wantRunResult: RunResult{
				Status:   StatusCompileError,
				Stdout:   "",
				ExitCode: exitCodeAny,
			},
			wantStderrContains: "error:",
		},
		{
			name: "java",
			runSpec: RunSpec{
				Language: "java",
				Code: `
public class Main {
	public static void main(String[] args) {
		System.out.println("Hello, World!")
	}
}`,
			},
			wantRunResult: RunResult{
				Status:   StatusCompileError,
				Stdout:   "",
				ExitCode: exitCodeAny,
			},
			wantStderrContains: "error:",
		},
		{
			name: "java",
			runSpec: RunSpec{
				Language: "java",
				Code: `
public class Oops {
	public static void main(String[] args) {
		System.out.println("hello");
	}
}`,
			},
			wantRunResult: RunResult{
				Status:   StatusCompileError,
				Stdout:   "",
				ExitCode: exitCodeAny,
			},
			wantStderrContains: "should be declared in a file named Oops.java",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SkipUnsupportedOnNsjail(t, tc.runSpec.Language)
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
	sb := NewTestSandboxFromEnv(t)

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

	var sb Sandbox
	switch os.Getenv(config.SandboxBackendEnv) {
	case "nsjail":
		// cgroup memory.max gives a clean OOM-kill; the rlimit_as fallback does not
		// (no clean 137), so the test only makes sense when cgroups are delegated.
		if !cgroupsActive() {
			t.Skip("nsjail cgroups unavailable (no delegation); OOM needs cgroup memory.max")
		}
		sb = NewTestSandbox(t, "nsjail")
	default:
		ds := NewTestSandbox(t, "docker")
		dsb := ds.(*DockerSandbox)
		// OOM kill requires Docker swap limit support. Without it Docker ignores
		// --memory-swap and the container spills to the swap partition instead of
		// being killed. Ask the daemon directly rather than guessing from cgroup paths.
		daemonInfo, err := dsb.client.Info(ctx, client.InfoOptions{})
		if err != nil || !daemonInfo.Info.SwapLimit {
			t.Skip("docker swap limits not supported on this system; skipping OOM test")
		}
		sb = dsb
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
	sb := NewTestSandboxFromEnv(t)

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

func TestRunWorkspaceFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := NewTestSandboxFromEnv(t)

	rs := RunSpec{
		Language: "python",
		Code: `
with open("data/in.txt") as f:
    print(f.read())
`,
		WorkspaceFiles: []WorkspaceFile{
			{
				Path:    "data/in.txt",
				Content: "hello",
			},
		},
	}
	want := RunResult{
		Status:   StatusOK,
		Stdout:   "hello\n",
		ExitCode: 0,
	}

	got, err := sb.Run(ctx, rs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ensureRunResult(t, got, want, "")
}

func TestRunConcurrency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := NewTestSandbox(t, "docker")

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

func anyImage(t *testing.T, sb *DockerSandbox) string {
	t.Helper()
	for _, entry := range sb.specs {
		if vs, ok := entry.versions[entry.defaultVersion]; ok {
			return vs.image
		}
	}
	t.Fatal("no image in registry")
	return ""
}

func waitForManagedContainer(ctx context.Context, t *testing.T, sb *DockerSandbox) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res, err := sb.client.ContainerList(ctx, client.ContainerListOptions{
			All:     true,
			Filters: client.Filters{}.Add("label", managedLabel),
		})
		if err == nil && len(res.Items) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no managed container appeared for the in-flight run")
}

func TestReapOrphansRemovesOrphan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := NewTestSandbox(t, "docker")
	dsb := sb.(*DockerSandbox)

	resp, err := dsb.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{Labels: map[string]string{managedLabel: "1"}},
		Image:  anyImage(t, dsb),
	})
	if err != nil {
		t.Fatalf("create orphan: %v", err)
	}
	// Safety net if the reap below fails to remove it.
	t.Cleanup(func() {
		dsb.client.ContainerRemove(context.Background(), resp.ID, client.ContainerRemoveOptions{Force: true})
	})

	dsb.reapOrphans(ctx, 0)

	if _, err := dsb.client.ContainerInspect(ctx, resp.ID, client.ContainerInspectOptions{}); err == nil {
		t.Fatal("orphan still present after reap")
	} else if !errdefs.IsNotFound(err) {
		t.Fatalf("inspect after reap: want not-found, got %v", err)
	}
}

func TestReapOrphansSparesLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	sb := NewTestSandbox(t, "docker")
	dsb := sb.(*DockerSandbox)

	resultCh := make(chan RunResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := sb.Run(ctx, RunSpec{
			Language: "python",
			Code:     "import time; time.sleep(3)\n",
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- res
	}()

	waitForManagedContainer(ctx, t, dsb)
	dsb.ReapOrphans(ctx) // real reapMaxAge (1m): the seconds-old live container is spared

	select {
	case res := <-resultCh:
		if res.Status != StatusOK || res.ExitCode != 0 {
			t.Fatalf("live run was disrupted by the reaper: status=%q exit=%d", res.Status, res.ExitCode)
		}
	case err := <-errCh:
		t.Fatalf("live run errored (reaper killed it?): %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for the live run to finish")
	}
}
