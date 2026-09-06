BINARY := argus

.PHONY: build run test

build:
	go build -o $(BINARY) ./cmd/argus

run: build
	./$(BINARY) version

test:
	go test ./...
