BIN ?= dist/codemesh

.PHONY: test build e2e e2e-packaged

test:
	go test ./...

build:
	mkdir -p "$(dir $(BIN))"
	go build -o "$(BIN)" ./cmd/codemesh

e2e:
	go run ./test/e2e

e2e-packaged: build
	CODEMESH_E2E_BINARY="$(abspath $(BIN))" CODEMESH_E2E_MODE=packaged go run ./test/e2e
