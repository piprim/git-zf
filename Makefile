VERSION := $(shell git describe --tags --exact-match 2>/dev/null || echo "dev")
TARGET     := git-zf
BIN_DIR := ./bin
BIN        := $(BIN_DIR)/$(TARGET)
LDFLAGS    := -ldflags "-X github.com/piprim/git-zf/cmd.Version=${VERSION}"

all: build

# No root: go install puts the binary in Go's bin directory, which git finds
# on PATH. The checks say which older binary, if any, would run instead.
install:
	CGO_ENABLED=0 go install $(LDFLAGS) .
	@dst="$$(go list -f '{{.Target}}' .)"; \
	printf 'Installed %s\n' "$$dst"; \
	exec_dir="$$(git --exec-path)"; \
	for f in "$$exec_dir/$(TARGET)" "$$exec_dir/$(TARGET).exe"; do \
		if [ -f "$$f" ] && ! [ "$$f" -ef "$$dst" ]; then \
			printf 'warning: git runs %s first; remove it with: sudo rm "%s"\n' "$$f" "$$f" >&2; \
		fi; \
	done; \
	found="$$(command -v $(TARGET) 2>/dev/null || true)"; \
	if [ -z "$$found" ]; then \
		printf 'warning: %s is not on PATH\n' "$$(dirname "$$dst")" >&2; \
	elif ! [ "$$found" -ef "$$dst" ]; then \
		printf 'warning: %s comes before %s on PATH\n' "$$found" "$$dst" >&2; \
	fi

uninstall:
	rm -f "$$(go list -f '{{.Target}}' .)"

clean:
	rm -rf $(BIN_DIR)

build:
	CGO_ENABLED=0 go build -o $(BIN) ${LDFLAGS}

test:
	go tool gotestsum --format testdox -- -v ./...

lint:
	golangci-lint run ./...

docs-gen:
	go run ./internal/docgen -out website/docs/reference/commands

docs-check:
	go test ./internal/docgen/...

docs: docs-gen
	cd website && npm ci && npm run build

docs-serve: docs-gen
	cd website && npm start

.PHONY: all install uninstall clean build test lint docs-gen docs-check docs docs-serve
