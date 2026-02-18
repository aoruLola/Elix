BRIDGE_BIN ?= elix-bridge
CODEX_ADAPTER_BIN ?= codex-adapter
GEMINI_ADAPTER_BIN ?= gemini-adapter
CLAUDE_ADAPTER_BIN ?= claude-adapter
ELIX_WALLET_BIN ?= elix-wallet

.PHONY: build
build:
	go build -o $(CODEX_ADAPTER_BIN) ./cmd/codex-adapter
	go build -o $(GEMINI_ADAPTER_BIN) ./cmd/gemini-adapter
	go build -o $(CLAUDE_ADAPTER_BIN) ./cmd/claude-adapter
	go build -o $(ELIX_WALLET_BIN) ./cmd/elix-wallet
	go build -o $(BRIDGE_BIN) ./cmd/bridge

.PHONY: run
run:
	./$(BRIDGE_BIN)

.PHONY: e2e-backends
e2e-backends: build
	bash ./scripts/e2e_backends.sh

.PHONY: pair-demo
pair-demo: build
	bash ./scripts/pair_demo.sh

.PHONY: install-systemd
install-systemd: build
	sudo bash ./scripts/install_systemd_bridge.sh

.PHONY: bootstrap
bootstrap: build
	sudo bash ./scripts/bootstrap_elix_bridge.sh

.PHONY: preflight
preflight:
	bash ./scripts/preflight_check.sh
