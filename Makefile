BINARY := fleetwatch
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test vet fmt example clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

## example: the exact command from the README, against the sample fleet
example: build
	./bin/$(BINARY) report \
		-vehicles examples/vehicles.csv \
		-drivers examples/drivers.csv \
		-operator "Pennine Haulage Ltd" \
		-today 2026-07-01

clean:
	rm -rf bin
