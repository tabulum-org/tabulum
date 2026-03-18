.PHONY: build test test-int run clean

# Build the kernel binary
build:
	go build -o bin/tabulum ./cmd/tabulum

# Run unit tests
test:
	go test ./internal/... -v -race

# Run integration tests
test-int:
	go test ./test/... -v -race -tags=integration

# Run the kernel (development)
run: build
	./bin/tabulum --config config.yaml

# Clean build artifacts
clean:
	rm -rf bin/
	rm -rf data/

# Format code
fmt:
	go fmt ./...

# Vet code
vet:
	go vet ./...

# Run all checks
check: fmt vet test
