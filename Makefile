GO ?= go
BIN := bin/tmuxrd

.PHONY: all build fakeagent test integration vet clean

all: build

# Build the main binary and the fake agent used by the integration suite.
build:
	$(GO) build -o $(BIN) ./cmd/tmuxrd
	$(GO) build -o cmd/fakeagent/fakeagent ./cmd/fakeagent

fakeagent:
	$(GO) build -o cmd/fakeagent/fakeagent ./cmd/fakeagent

# Unit tests cover session identity. The tmux behaviour lives in the godog suite.
test:
	$(GO) test ./...

# Godog/Gherkin integration suite. Hermetic: every scenario runs on its own
# tmux -L godog-* socket and never touches the default server.
integration: build
	$(GO) test -tags integration -count=1 -run TestGodog ./...

vet:
	$(GO) vet ./...
	$(GO) vet -tags integration ./...

clean:
	rm -f $(BIN) cmd/fakeagent/fakeagent
