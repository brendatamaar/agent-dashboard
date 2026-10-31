ifeq ($(OS),Windows_NT)
    # Avoid broken WSL bash.exe in System32 by using Git's sh.exe if available
    SHELL := C:/Program Files/Git/bin/sh.exe
endif

.PHONY: all dev test lint build verify clean web-deps check-core

all: build

web-deps: web/node_modules/.package-lock.json

web/node_modules/.package-lock.json: web/package.json web/package-lock.json
	cd web && npm ci

dev: web-deps
	@echo "Starting backend and frontend in dev mode..."
	node scripts/dev.mjs

test: web-deps
	go test -race ./...
	cd web && npm test

lint: web-deps
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; fi
	cd web && npm run lint
	$(MAKE) check-core

check-core:
	node .agents/scripts/fe-check.mjs scan

build: web-deps
	cd web && npm run build
	go build -o agent-dashboard ./cmd/agent-dashboard

verify: lint test build
	@echo "=== Verification complete: ALL CHECKS PASSED ==="

clean:
	rm -f agent-dashboard agent-dashboard.exe
	rm -rf web/dist
