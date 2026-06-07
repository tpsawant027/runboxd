BINARY=bin/runboxd


.PHONY: build run test integration lint clean images
build:
	go build -o $(BINARY) ./cmd/runboxd
run: build
	./$(BINARY)
test:
	go test ./...
images:
	docker build -t runboxd-python:latest images/python
integration: images
	go test -tags=integration -race ./...
lint:
	go vet ./...
clean:
	rm -rf bin/
