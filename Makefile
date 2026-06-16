.PHONY: all web hub agent build test e2e run-hub run-agent clean

# Rolling version: build timestamp yyyyMMddhhmm
VERSION ?= $(shell date -u +%Y%m%d%H%M)
LDFLAGS := -s -w -X main.version=$(VERSION)

all: build

# Build the web console into sys0-hub/web (embedded by the hub binary).
web:
	cd sys0-console && npm install && npm run build

hub:
	go build -ldflags "$(LDFLAGS)" -o bin/sys0-hub ./sys0-hub/

agent:
	go build -ldflags "$(LDFLAGS)" -o bin/sys0-agent ./sys0-agent/

# Full build: console first (so go:embed picks up fresh assets), then binaries.
build: web hub agent

# Go unit/integration tests.
test:
	go test ./...

# End-to-end smoke test against the real binaries (builds Go, not the web).
e2e:
	bash scripts/e2e.sh

run-hub: hub
	./bin/sys0-hub -http :8080 -agent-tcp :7000 -key devkey

run-agent: agent
	./bin/sys0-agent -hub 127.0.0.1:7000 -transport tcp -key devkey -label dev

clean:
	rm -rf bin sys0-console/node_modules *.db *.log
