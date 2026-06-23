BIN ?= dist/codemesh

.PHONY: test build e2e e2e-packaged docs\:list docs-list

test:
	go test ./...

build:
	mkdir -p "$(dir $(BIN))"
	go build -o "$(BIN)" ./cmd/codemesh

e2e:
	go run ./test/e2e

e2e-packaged: build
	CODEMESH_E2E_BINARY="$(abspath $(BIN))" CODEMESH_E2E_MODE=packaged go run ./test/e2e

docs\:list: docs-list

docs-list:
	@if [ -x "./bin/docs-list" ]; then \
		./bin/docs-list .; \
	elif command -v docs-list >/dev/null 2>&1; then \
		docs-list .; \
	else \
		echo "docs-list helper not found" >&2; \
		exit 1; \
	fi
