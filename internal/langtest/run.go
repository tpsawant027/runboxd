package langtest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tpsawant027/runboxd/internal/sandbox"
)

func runBudget(rs sandbox.RunSpec) time.Duration {
	if rs.Timeout > 0 {
		return rs.Timeout + 30*time.Second
	}
	return 60 * time.Second
}

func RunFixture(ctx context.Context, sb sandbox.Sandbox, fx Fixture) []CaseResult {
	results := make([]CaseResult, 0, len(fx.ConformanceTests)+len(fx.SmokeTests))
	for k, v := range CapabilityStatus {
		if ct, ok := fx.ConformanceTests[k]; ok {
			rs := sandbox.RunSpec{
				Language: fx.Language,
				Version:  fx.Version,
				Code:     ct.Source,
			}
			if k == "oom" {
				if ct.MemoryBytes <= 0 {
					results = append(results, CaseResult{
						Name:   "conformance/" + k,
						Passed: false,
						Detail: "invalid test case: memory_bytes must be > 0",
					})
					continue
				}
				rs.MemoryBytes = ct.MemoryBytes
			}
			if k == "timeout" {
				if ct.TimeoutMS <= 0 {
					results = append(results, CaseResult{
						Name:   "conformance/" + k,
						Passed: false,
						Detail: "invalid test case: timeout_ms must be > 0",
					})
					continue
				}
				rs.Timeout = time.Duration(ct.TimeoutMS) * time.Millisecond
			}
			runCtx, cancel := context.WithTimeout(ctx, runBudget(rs))
			got, err := sb.Run(runCtx, rs)
			cancel()
			if err != nil {
				results = append(results, CaseResult{
					Name:   "conformance/" + k,
					Passed: false,
					Detail: fmt.Sprintf("Run returned internal error: %v", err),
				})
				continue
			}
			passed := true
			var details strings.Builder
			if got.Status != v {
				passed = false
				fmt.Fprintf(&details, "got status %q, want %q; ", got.Status, v)
			}
			if ct.WantStderrContains != "" {
				if !strings.Contains(string(got.Stderr), ct.WantStderrContains) {
					passed = false
					fmt.Fprintf(&details, "stderr = %q, want to contain %q; ", got.Stderr, ct.WantStderrContains)
				}
			}
			results = append(results, CaseResult{
				Name:   "conformance/" + k,
				Passed: passed,
				Detail: details.String(),
			})
		}
	}

	for _, st := range fx.SmokeTests {
		rs := sandbox.RunSpec{
			Language: fx.Language,
			Version:  fx.Version,
			Code:     st.Source,
		}
		if st.Stdin != "" {
			rs.Stdin = st.Stdin
		}
		if st.TimeoutMS > 0 {
			rs.Timeout = time.Duration(st.TimeoutMS) * time.Millisecond
		}
		if st.MemoryBytes > 0 {
			rs.MemoryBytes = st.MemoryBytes
		}
		if len(st.Files) > 0 {
			files := make([]sandbox.WorkspaceFile, 0, len(st.Files))
			for _, f := range st.Files {
				files = append(files, sandbox.WorkspaceFile{
					Path:    f.Name,
					Content: f.Content,
				})
			}
			rs.WorkspaceFiles = files
		}
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		got, err := sb.Run(runCtx, rs)
		cancel()
		if err != nil {
			results = append(results, CaseResult{
				Name:   "smoke/" + st.Name,
				Passed: false,
				Detail: fmt.Sprintf("Run returned internal error: %v", err),
			})
			continue
		}
		wantStatus := st.WantStatus
		if wantStatus == "" {
			wantStatus = "ok"
		}
		passed := true
		var details strings.Builder
		if string(got.Status) != wantStatus {
			passed = false
			fmt.Fprintf(&details, "got status %q, want %q; ", got.Status, wantStatus)
		}
		if st.WantExitCode != nil {
			if got.ExitCode != *st.WantExitCode {
				passed = false
				fmt.Fprintf(&details, "got exit code %d, want %d; ", got.ExitCode, *st.WantExitCode)
			}
		}
		if st.WantStdout != "" {
			if got.Stdout != st.WantStdout {
				passed = false
				fmt.Fprintf(&details, "got stdout %q, want %q; ", got.Stdout, st.WantStdout)
			}
		}
		if st.WantStdoutContains != "" {
			if !strings.Contains(string(got.Stdout), st.WantStdoutContains) {
				passed = false
				fmt.Fprintf(&details, "got stdout %q, want to contain %q; ", got.Stdout, st.WantStdoutContains)
			}
		}
		if st.WantStderrContains != "" {
			if !strings.Contains(string(got.Stderr), st.WantStderrContains) {
				passed = false
				fmt.Fprintf(&details, "got stderr %q, want to contain %q; ", got.Stderr, st.WantStderrContains)
			}
		}
		results = append(results, CaseResult{
			Name:   "smoke/" + st.Name,
			Passed: passed,
			Detail: details.String(),
		})
	}

	return results
}
