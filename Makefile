VERSION := $(shell git describe --tags --exact-match 2>/dev/null || echo "dev")
TARGET     := git-zf
BIN_DIR := ./bin
BIN        := $(BIN_DIR)/$(TARGET)
GOARCH := $(shell go env GOARCH)
LDFLAGS    := -ldflags "\
  -X github.com/piprim/git-zf/cmd.Version=${VERSION} \
  -X github.com/piprim/git-zf/cmd.Name=${TARGET}"


ifeq ($(OS),Windows_NT)
	GOOS := windows
	COPY := copy
else
	COPY := cp
	UNAME_S := $(shell uname -s)
	ifeq ($(UNAME_S),Linux)
		GOOS := linux
	else ifeq ($(UNAME_S),Darwin)
		GOOS := darwin
	endif
endif

GIT_EXEC_PATH := $(shell git --exec-path)

all: build
install: build
	$(COPY) $(BIN) $(GIT_EXEC_PATH)/$(TARGET)
clean:
	rm -rf $(BIN_DIR)

build:
	CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -o $(BIN) ${LDFLAGS}

test:
	go test ./...

.PHONY: all install clean build test
