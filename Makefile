VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X github.com/beaconclaw/beaconclaw/internal/agent.Version=$(VERSION)"
BINARY := beaconclaw
BUILD_DIR := bin

.PHONY: build test lint clean cross-compile checksums install

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/beaconclaw

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BUILD_DIR)

install: build
	cp $(BUILD_DIR)/$(BINARY) $(GOPATH)/bin/$(BINARY) 2>/dev/null || sudo cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	ln -sf $(BINARY) $(GOPATH)/bin/bclaw 2>/dev/null || sudo ln -sf /usr/local/bin/$(BINARY) /usr/local/bin/bclaw

# Cross-compile for all target platforms
cross-compile:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/beaconclaw
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/beaconclaw
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/beaconclaw
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/beaconclaw
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/beaconclaw
	GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-windows-arm64.exe ./cmd/beaconclaw

# Write bin/checksums.txt for the cross-compiled binaries, in sha256sum format.
# The installer and `beaconclaw update` verify downloads against this file.
checksums:
	cd $(BUILD_DIR) && rm -f checksums.txt && \
		(sha256sum $(BINARY)-*-* 2>/dev/null || shasum -a 256 $(BINARY)-*-*) > checksums.txt
