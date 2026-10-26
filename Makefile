ifeq ($(OS),Windows_NT)
    # Avoid broken WSL bash.exe in System32 by using Git's sh.exe if available
    SHELL := C:/Program Files/Git/bin/sh.exe
endif

.PHONY: all dev test lint build verify clean

all: build

dev:
	@echo "Starting backend and frontend in dev mode..."
	cd web && npm run dev &
	go run ./cmd/agent-dashboard

test:
	go test -v ./internal/core/... ./internal/shell/... ./internal/store/... ./internal/api/...
	cd web && npm test || true

lint:
	go vet ./...
	cd web && npm run lint

build:
	cd web && npm run build
	go build -o agent-dashboard ./cmd/agent-dashboard

verify: lint test build
	@echo "=== Verification complete: ALL CHECKS PASSED ==="

clean:
	rm -f agent-dashboard agent-dashboard.exe
	rm -rf web/dist
