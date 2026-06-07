BINARY=bin/runboxd


.PHONY: build run test integration lint clean gen-lock gen-images images
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
	docker build -t runboxd-python:latest images/python/3.14
integration: images
	go test -tags=integration -race ./...
lint:
	go vet ./...
clean:
	rm -rf bin/
