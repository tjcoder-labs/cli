CODER_BIN   := bin/coder
PKG_JSON    := package.json
EMBED_PKG   := cmd/coder/package.json
VERSION     := $(shell python3 -c 'import json; print(json.load(open("$(PKG_JSON)"))["version"])' 2>/dev/null || echo dev)
PRODUCT     := $(shell python3 -c 'import json,sys; d=json.load(open("$(PKG_JSON)")); print(d.get("productName") or d.get("name") or "Coder CLI")' 2>/dev/null || echo "Coder CLI")
AUTHOR      := $(shell python3 -c 'import json; print(json.load(open("$(PKG_JSON)"))["author"])' 2>/dev/null || echo "TJ Coder AI Labs")
LDFLAGS     := -X 'main.version=$(VERSION)' -X 'main.productName=$(PRODUCT)' -X 'main.author=$(AUTHOR)'

.PHONY: build build-coder run run-coder fmt clean install uninstall

build: $(EMBED_PKG)
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o $(CODER_BIN) ./cmd/coder

# Same source, installed as `coder` (the canonical CLI name).
build-coder: build

# cmd/coder/main.go uses //go:embed package.json. go:embed forbids path
# traversal, so we stage a sibling copy of the root package.json in the cmd
# dir for the embed to resolve cleanly.
$(EMBED_PKG): $(PKG_JSON)
	cp $(PKG_JSON) $(EMBED_PKG)

run: build
	./$(CODER_BIN)

run-coder: build-coder
	./$(CODER_BIN)

# `make install` builds `coder` and drops it into ~/.local/bin (no sudo).
# Use `PREFIX=/usr/local/bin sudo make install` for a system-wide install.
install: build-coder
	@PREFIX="$${PREFIX:-$$HOME/.local/bin}"; \
	mkdir -p "$$PREFIX" || { echo "cannot create $$PREFIX (try PREFIX=$$HOME/.local/bin or run with sudo)"; exit 1; }; \
	install -m 0755 $(CODER_BIN) "$$PREFIX/coder"; \
	echo "installed $$PREFIX/coder (v$(VERSION))"

uninstall:
	@PREFIX="$${PREFIX:-$$HOME/.local/bin}"; \
	rm -f "$$PREFIX/coder" && echo "removed $$PREFIX/coder"

fmt:
	gofmt -w ./cmd ./internal

test: TESTS.sh
	bash TESTS.sh

clean:
	rm -f $(EMBED_PKG)
	rm -rf bin
