BINARY := argus

.PHONY: build run test test-integration

build:
	go build -o $(BINARY) ./cmd/argus

run: build
	./$(BINARY) version

test:
	go test ./...

# Requires Docker and a pg_dump on PATH at least as new as the server image
# the tests start.
test-integration:
	go test -tags=integration ./test/integration/... -count=1 -timeout 10m
