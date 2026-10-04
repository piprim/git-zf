VERSION := $(shell git describe --tags --exact-match 2>/dev/null || echo "dev")
TARGET     := git-zf
BIN_DIR := ./bin
BIN        := $(BIN_DIR)/$(TARGET)
LDFLAGS    := -ldflags "-X github.com/piprim/git-zf/cmd.Version=${VERSION}"


ifeq ($(OS),Windows_NT)
	COPY := copy
	RM := del /Q /F
else
	COPY := cp
	RM := rm -f
endif

GIT_EXEC_PATH := $(shell git --exec-path)

all: build
install: build
	$(COPY) $(BIN) $(GIT_EXEC_PATH)/$(TARGET)

uninstall:
	$(RM) $(GIT_EXEC_PATH)/$(TARGET)

clean:
	rm -rf $(BIN_DIR)

build:
	CGO_ENABLED=0 go build -o $(BIN) ${LDFLAGS}

test:
	go tool gotestsum --format testdox -- -v ./...

lint:
	golangci-lint run ./...

.PHONY: all install clean build test lint
