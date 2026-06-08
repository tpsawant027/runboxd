package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type spyNext struct {
	called bool
	fn     func(http.ResponseWriter, *http.Request)
}

func (s *spyNext) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.called = true
	if s.fn != nil {
		s.fn(w, r)
	}
}

func runWorkerPoolMiddleware(pool *WorkerPool, req *http.Request, fn func(http.ResponseWriter, *http.Request)) (*httptest.ResponseRecorder, *spyNext) {
	w := httptest.NewRecorder()
	spy := &spyNext{fn: fn}
	pool.Middleware()(spy).ServeHTTP(w, req)
	return w, spy
}

func newExecRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/execute", nil)
}

func TestWorkerPoolMiddleware(t *testing.T) {
	cases := []struct {
		name           string
		poolSize       int
		poolMaxWaiting int
		preFillWorkers bool
		wantStatus     int
		wantNextCalled bool
	}{
		{
			name:           "allows request when workers are available",
			poolSize:       1,
			poolMaxWaiting: 0,
			preFillWorkers: false,
			wantStatus:     http.StatusOK,
			wantNextCalled: true,
		},
		{
			name:           "returns 429 when pool is full and no waiting allowed",
			poolSize:       1,
			poolMaxWaiting: 0,
			preFillWorkers: true,
			wantStatus:     http.StatusTooManyRequests,
			wantNextCalled: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewWorkerPool(tc.poolSize, tc.poolMaxWaiting)
			if tc.preFillWorkers {
				for range tc.poolSize {
					pool.sem <- struct{}{}
				}
				for range tc.poolMaxWaiting {
					pool.waiting.Add(1)
				}
			}

			w, next := runWorkerPoolMiddleware(pool, newExecRequest(), nil)

			if w.Code != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
			if next.called != tc.wantNextCalled {
				t.Errorf("expected next called to be %v, got %v", tc.wantNextCalled, next.called)
			}
		})
	}
}

func TestWorkerPoolQueued(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	doneA := make(chan struct{})
	doneB := make(chan struct{})

	var gotStatusA, gotStatusB int

	pool := NewWorkerPool(1, 1)

	go func() {
		w, _ := runWorkerPoolMiddleware(pool, newExecRequest(), func(_ http.ResponseWriter, _ *http.Request) {
			entered <- struct{}{}
			<-release
		})
		gotStatusA = w.Code
		close(doneA)
	}()

	<-entered

	go func() {
		w, _ := runWorkerPoolMiddleware(pool, newExecRequest(), nil)
		gotStatusB = w.Code
		close(doneB)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pool.waiting.Load() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	close(release)

	<-doneA
	<-doneB

	if gotStatusA != http.StatusOK {
		t.Errorf("expected first request status %d, got %d", http.StatusOK, gotStatusA)
	}
	if gotStatusB != http.StatusOK {
		t.Errorf("expected second request status %d, got %d", http.StatusOK, gotStatusB)
	}
}

func TestWorkerPoolContextCancelled(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	doneA := make(chan struct{})
	doneB := make(chan struct{})

	var gotStatusA, gotStatusB int

	pool := NewWorkerPool(1, 1)

	go func() {
		w, _ := runWorkerPoolMiddleware(pool, newExecRequest(), func(_ http.ResponseWriter, _ *http.Request) {
			entered <- struct{}{}
			<-release
		})
		gotStatusA = w.Code
		close(doneA)
	}()

	<-entered

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		w, _ := runWorkerPoolMiddleware(pool, newExecRequest().WithContext(ctx), nil)
		gotStatusB = w.Code
		close(doneB)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if pool.waiting.Load() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	close(release)

	<-doneA
	<-doneB

	if gotStatusA != http.StatusOK {
		t.Errorf("expected first request status %d, got %d", http.StatusOK, gotStatusA)
	}
	if gotStatusB != http.StatusRequestTimeout {
		t.Errorf("expected second request status %d, got %d", http.StatusRequestTimeout, gotStatusB)
	}
}
