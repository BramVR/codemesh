.PHONY: test e2e

test:
	go test ./...

e2e:
	go run ./test/e2e
