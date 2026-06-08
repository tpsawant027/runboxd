BINARY=bin/runboxd


LOAD_RATES ?= 5 8 20 50
LOAD_DURATION ?= 15s

.PHONY: build run test integration load lint clean gen-lock gen-images images
build:
	go build -o $(BINARY) ./cmd/runboxd
run: build
	./$(BINARY)
test:
	go test ./...
gen-lock:
	go run ./cmd/genlock -dir ./images -out ./images.lock.yml
gen-images:
	go run ./cmd/imagegen -dir ./images -lockfile ./images.lock.yml -registry-out ./language_registry.yml
images: gen-images
	go run ./cmd/buildimages -dir ./images -registry ./language_registry.yml
integration: images
	go test -tags=integration -race ./...
# Load test the running server (start it first, e.g. `make run`). Sweeps a few
# request rates and reports latency + the status-code split per rate.
# Override: make load LOAD_RATES="10 100" LOAD_DURATION=30s
load:
	@command -v vegeta >/dev/null || { echo "vegeta not found: go install github.com/tsenart/vegeta@latest"; exit 1; }
	@for r in $(LOAD_RATES); do \
		echo "==================== rate=$$r/s, $(LOAD_DURATION) ===================="; \
		vegeta attack -targets=loadtest/targets.txt -rate=$$r -duration=$(LOAD_DURATION) | vegeta report; \
	done
lint:
	go vet ./...
clean:
	rm -rf bin/
