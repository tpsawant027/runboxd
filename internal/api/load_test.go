package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tpsawant027/runboxd/internal/sandbox"
)

type loadFakeSandbox struct {
	langSpec  sandbox.LangSpec
	runResult sandbox.RunResult

	inFlight    atomic.Int32  // currently inside Run
	maxInFlight atomic.Int32  // high-water mark
	total       atomic.Int64  // completed Runs
	gate        chan struct{} // if non-nil, Run blocks until it's closed
}

func (f *loadFakeSandbox) LangSpec(_, _ string) (sandbox.LangSpec, error) { return f.langSpec, nil }
func (f *loadFakeSandbox) Close() error                                   { return nil }

func (f *loadFakeSandbox) Run(ctx context.Context, _ sandbox.RunSpec) (sandbox.RunResult, error) {
	n := f.inFlight.Add(1)
	for { // monotonic high-water update
		m := f.maxInFlight.Load()
		if n <= m || f.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	defer f.inFlight.Add(-1)
	if f.gate != nil {
		<-f.gate // hold in-flight until the test releases
	}
	f.total.Add(1)
	return f.runResult, nil
}

func BenchmarkExecute(b *testing.B) {
	sb := &loadFakeSandbox{
		langSpec: sandbox.LangSpec{
			Filename: "main.py",
			Limits: sandbox.LangLimits{
				MinTimeout:         sandbox.MinTimeout,
				MaxTimeout:         sandbox.MaxTimeout,
				MinMemoryBytes:     sandbox.MinMemoryBytes,
				MaxMemoryBytes:     sandbox.MaxMemoryBytes,
				MaxPids:            sandbox.MaxPids,
				MaxCPUs:            sandbox.DefaultMaxCPUs,
				WorkspaceSizeBytes: sandbox.DefaultWorkspaceSizeMiB * 1024 * 1024,
				TmpSizeBytes:       sandbox.DefaultTmpSizeMiB * 1024 * 1024,
			},
		},
		runResult: sandbox.RunResult{
			Stdout:   "Hello, World!\n",
			ExitCode: 0,
			Status:   sandbox.StatusOK,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := NewWorkerPool(runtime.NumCPU()*2, 1<<16)
	srv := NewServer(logger, "", sb, pool)
	req := ExecuteRequest{
		Language: "python",
		Code:     "print('Hello, World!')",
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		b.Fatalf("Failed to marshal request: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			testReq := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(reqBody))
			w := httptest.NewRecorder()
			srv.Routes().ServeHTTP(w, testReq)
			if w.Code != http.StatusOK {
				b.Fatalf("Expected status 200, got %d", w.Code)
			}
		}
	})
}

func TestExecuteUnderLoad(t *testing.T) {
	const (
		poolSize    = 4
		maxWaiting  = 8
		concurrency = 200
	)

	sb := &loadFakeSandbox{
		langSpec: sandbox.LangSpec{
			Filename: "main.py",
			Limits: sandbox.LangLimits{
				MinTimeout:         sandbox.MinTimeout,
				MaxTimeout:         sandbox.MaxTimeout,
				MinMemoryBytes:     sandbox.MinMemoryBytes,
				MaxMemoryBytes:     sandbox.MaxMemoryBytes,
				MaxPids:            sandbox.MaxPids,
				MaxCPUs:            sandbox.DefaultMaxCPUs,
				WorkspaceSizeBytes: sandbox.DefaultWorkspaceSizeMiB * 1024 * 1024,
				TmpSizeBytes:       sandbox.DefaultTmpSizeMiB * 1024 * 1024,
			},
		},
		runResult: sandbox.RunResult{
			Stdout:   "Hello, World!\n",
			ExitCode: 0,
			Status:   sandbox.StatusOK,
		},
		gate: make(chan struct{}),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := NewWorkerPool(poolSize, maxWaiting)
	srv := NewServer(logger, "", sb, pool)

	req := ExecuteRequest{
		Language: "python",
		Code:     "print('Hello, World!')",
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	wantOK := poolSize + maxWaiting
	wantShed := concurrency - wantOK

	codes := make([]int, concurrency)
	var wg, readyWG sync.WaitGroup
	var shedCount atomic.Int32 // completed (pre-release) responses; only shed requests can finish before the gate opens
	wg.Add(concurrency)
	readyWG.Add(concurrency)
	start := make(chan struct{})
	for i := range concurrency {
		go func(i int) {
			defer wg.Done()
			testReq := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(reqBody))
			w := httptest.NewRecorder()
			readyWG.Done()
			<-start // all goroutines fire together, so none can arrive late
			srv.Routes().ServeHTTP(w, testReq)
			codes[i] = w.Code
			shedCount.Add(1)
		}(i)
	}

	readyWG.Wait() // every goroutine is spawned and parked at the gate
	close(start)

	// Poll until exactly wantShed requests have already been shed
	// with a final 429 response. Since the pool never releases a slot before
	// we close the gate, nothing can finish early except a shed request, so
	// this count reaching wantShed proves all 200 requests have made their
	// admission decision (the remaining wantOK are parked in Run/the wait
	// queue) before we release them.
	deadline := time.Now().Add(2 * time.Second)
	for shedCount.Load() != int32(wantShed) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := shedCount.Load(); got != int32(wantShed) {
		t.Fatalf("not all excess requests were shed before release: shed = %d, want %d", got, wantShed)
	}
	if got := sb.inFlight.Load(); got != poolSize {
		t.Fatalf("pool never saturated: inFlight = %d, want %d", got, poolSize)
	}
	if got := pool.waiting.Load(); got != maxWaiting {
		t.Fatalf("wait queue never filled: waiting = %d, want %d", got, maxWaiting)
	}

	close(sb.gate)
	wg.Wait()

	if got := sb.maxInFlight.Load(); got != poolSize {
		t.Errorf("maxInFlight = %d, want %d (cap did not hold)", got, poolSize)
	}

	var got200, got429 int
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			got200++
		case http.StatusTooManyRequests:
			got429++
		default:
			t.Errorf("codes[%d] = %d, want 200 or 429", i, code)
		}
	}

	if got200 != wantOK {
		t.Errorf("got %d responses with status 200, want %d", got200, wantOK)
	}
	if got429 != wantShed {
		t.Errorf("got %d responses with status 429, want %d", got429, wantShed)
	}
}
