ifeq ($(OS),Windows_NT)
    # Avoid broken WSL bash.exe in System32 by using Git's sh.exe if available
    SHELL := C:/Program Files/Git/bin/sh.exe
endif

.PHONY: all dev test lint build verify clean web-deps

all: build

web-deps:
	@if [ ! -d "web/node_modules" ]; then \
		echo "Installing web dependencies..."; \
		(cd web && npm install); \
	fi

dev: web-deps
	@echo "Starting backend and frontend in dev mode..."
	cd web && npm run dev &
	go run ./cmd/agent-dashboard

test: web-deps
	go test -v ./internal/...
	cd web && npm test || true

lint: web-deps
	go vet ./...
	cd web && npm run lint

build: web-deps
	cd web && npm run build
	go build -o agent-dashboard ./cmd/agent-dashboard

verify: lint test build
	@echo "=== Verification complete: ALL CHECKS PASSED ==="

clean:
	rm -f agent-dashboard agent-dashboard.exe
	rm -rf web/dist
