.PHONY: build run test vet clean

# Build the agent binary
build:
	go build -o agentd.exe ./cmd/agentd

# Build for Linux (k3s Phase 3)
build-linux:
	GOOS=linux GOARCH=amd64 go build -o agentd ./cmd/agentd

# Run locally
run:
	go run ./cmd/agentd

# Run all tests
test:
	go test ./... -v -count=1

# Vet for code issues
vet:
	go vet ./...

# Tidy modules
tidy:
	go mod tidy

# Clean build artifacts
clean:
	rm -f agentd.exe agentd
