.PHONY: build vet test race staticcheck check

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

staticcheck:
	staticcheck ./...

check: build vet race staticcheck
