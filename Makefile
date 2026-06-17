BINARY=bin/runboxd


LOAD_RATES ?= 5 8 20 50
LOAD_DURATION ?= 15s

COVER_PROFILE ?= cover.out

.PHONY: build run test cover integration integration-nsjail load lint clean gen-lock gen-images images rootfs adversarial adversarial-nsjail conformance conformance-nsjail
build:
	go build -o $(BINARY) ./cmd/runboxd
run: build
	./$(BINARY)
test:
	go test ./...
# For coverage of the Docker-dependent Run path, add the integration tag: make cover GOFLAGS=-tags=integration
cover:
	go test $(GOFLAGS) -covermode=atomic -coverprofile=$(COVER_PROFILE) ./...
	go tool cover -func=$(COVER_PROFILE)
	go tool cover -html=$(COVER_PROFILE) -o $(COVER_PROFILE:.out=.html)
gen-lock:
	go run ./cmd/runboxctl gen-lock --image-dir ./images --lockfile ./images.lock.yml
gen-images:
	go run ./cmd/runboxctl gen-images --image-dir ./images --lockfile ./images.lock.yml --registry ./language_registry.yml
images: gen-images
	go run ./cmd/runboxctl build-images --image-dir ./images --registry ./language_registry.yml
rootfs: images
	-chmod -R u+w _rootfs 2>/dev/null
	go run ./cmd/runboxctl export-rootfs --registry ./language_registry.yml --rootfs-dir ./_rootfs
integration: images
	go test -tags=integration -race -timeout 5m ./...
integration-nsjail: rootfs
	go test -c -race -tags=integration -o /tmp/runboxd-sandbox.test ./internal/sandbox
	cd internal/sandbox && systemd-run --user --scope -p Delegate=yes env SANDBOX_BACKEND=nsjail /tmp/runboxd-sandbox.test -test.run TestRun -test.timeout 5m
adversarial: images
	go test -tags=adversarial -race -timeout 5m ./internal/sandbox -run TestAdv
adversarial-nsjail: rootfs
	SANDBOX_BACKEND=nsjail go test -tags=adversarial -race -timeout 5m ./internal/sandbox -run TestAdv
conformance: images
	go test -tags=conformance -race -timeout 5m ./internal/langtest -run TestConformance
conformance-nsjail: rootfs
	go test -c -race -tags=conformance -o /tmp/runboxd-langtest.test ./internal/langtest
	cd internal/langtest && systemd-run --user --scope -p Delegate=yes env SANDBOX_BACKEND=nsjail /tmp/runboxd-langtest.test -test.run TestConformance -test.timeout 5m
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
	rm -f $(COVER_PROFILE) $(COVER_PROFILE:.out=.html)
