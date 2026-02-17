BRIDGE_BIN ?= bridge
CODEX_ADAPTER_BIN ?= codex-adapter
GEMINI_ADAPTER_BIN ?= gemini-adapter
CLAUDE_ADAPTER_BIN ?= claude-adapter

.PHONY: build
build:
	go build -o $(CODEX_ADAPTER_BIN) ./cmd/codex-adapter
	go build -o $(GEMINI_ADAPTER_BIN) ./cmd/gemini-adapter
	go build -o $(CLAUDE_ADAPTER_BIN) ./cmd/claude-adapter
	go build -o $(BRIDGE_BIN) ./cmd/bridge

.PHONY: run
run:
	./$(BRIDGE_BIN)
