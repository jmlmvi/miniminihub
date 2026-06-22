.PHONY: proto tidy build build-linux stub clean

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -s -w"

# Génère les stubs gRPC depuis proto/miniminihub.proto
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
	       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	       proto/mmhpb/miniminihub.proto

tidy:
	go mod tidy

# Binaire agent (host)
build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/miniminihub ./cmd/miniminihub/

# Binaire agent statique pour la cible de déploiement (VPS OVH amd64)
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/miniminihub-linux-amd64 ./cmd/miniminihub/

# Parent-stub (Phase 0 — preuve de tunnel)
stub:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/parent-stub-linux-amd64 ./cmd/parent-stub/

clean:
	rm -rf bin/
