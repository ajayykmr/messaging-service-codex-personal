.PHONY: tidy lint test

## tidy: Ensure go.mod and go.sum are up to date.
tidy:
go mod tidy

## lint: Placeholder for linting commands.
lint:
@echo "lint target not yet implemented"

## test: Placeholder for running unit tests.
test:
go test ./...
