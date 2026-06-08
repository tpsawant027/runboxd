package api

import (
	"net/http"
	"sync/atomic"
)

type WorkerPool struct {
	sem        chan struct{}
	waiting    atomic.Int32
	maxWaiting int32
}

func NewWorkerPool(size, maxWaiting int) *WorkerPool {
	return &WorkerPool{
		sem:        make(chan struct{}, size),
		maxWaiting: int32(maxWaiting),
	}
}

func (p *WorkerPool) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case p.sem <- struct{}{}:
			default:
				if p.waiting.Add(1) > p.maxWaiting {
					p.waiting.Add(-1)
					writeError(w, http.StatusTooManyRequests, "Worker pool is full, try again later")
					return
				}
				select {
				case p.sem <- struct{}{}:
					p.waiting.Add(-1)
				case <-r.Context().Done():
					p.waiting.Add(-1)
					writeError(w, http.StatusRequestTimeout, "Request timed out while waiting for worker")
					return
				}
			}
			defer func() { <-p.sem }()
			next.ServeHTTP(w, r)
		})
	}
}
