.PHONY: build binaries vet test race staticcheck check

build:
	go build ./...

# Named binaries for a clean checkout (sibling discovery: simurgh-shim beside simurgh).
binaries:
	go build -o simurgh ./cmd/simurgh
	go build -o simurgh-shim ./cmd/simurgh-shim

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

staticcheck:
	staticcheck ./...

check: build vet race staticcheck
