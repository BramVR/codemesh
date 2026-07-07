BIN ?= dist/codemesh

.PHONY: test build e2e e2e-packaged e2e-live e2e-peekaboo e2e-owned-host crabbox-pr-proof docs\:list docs-list docs-site docs-site-test docs-site-clean

test:
	go test ./...

build:
	mkdir -p "$(dir $(BIN))"
	go build -o "$(BIN)" ./cmd/codemesh

e2e:
	go run ./test/e2e

e2e-packaged: build
	CODEMESH_E2E_BINARY="$(abspath $(BIN))" CODEMESH_E2E_MODE=packaged go run ./test/e2e

e2e-live:
	CODEMESH_E2E_MODE=live go run ./test/e2e

e2e-peekaboo: build
	CODEMESH_E2E_BINARY="$(abspath $(BIN))" CODEMESH_E2E_MODE=live CODEMESH_E2E_LIVE=1 CODEMESH_E2E_LIVE_TARGETS=desktop go run ./test/e2e

e2e-owned-host: build
	CODEMESH_E2E_BINARY="$(abspath $(BIN))" CODEMESH_E2E_MODE=live CODEMESH_E2E_LIVE=1 CODEMESH_E2E_LIVE_TARGETS=owned-host go run ./test/e2e

crabbox-pr-proof:
	scripts/crabbox-pr-proof

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

docs-site:
	node scripts/build-docs-site.mjs

docs-site-test:
	node --test scripts/build-docs-site.test.mjs

docs-site-clean:
	rm -rf dist/docs-site
