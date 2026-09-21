.PHONY: build build-dev install test vet clean cross

# The five grammar_subset_<lang> tags pick exactly the languages crocs
# parses. Together with `grammar_subset`, this drops the gotreesitter
# binary footprint from ~30MB (all 206 grammars) to ~6MB (just these five).
TAGS    := grammar_subset grammar_subset_python grammar_subset_typescript grammar_subset_tsx grammar_subset_java grammar_subset_go
LDFLAGS := -s -w

# Canonical release build: stripped, trimmed, subset-embedded grammars,
# CGO disabled. This is the binary `make install` puts on $PATH.
build:
	CGO_ENABLED=0 go build \
		-tags '$(TAGS)' \
		-ldflags="$(LDFLAGS)" \
		-trimpath \
		-o bin/crocs ./cmd/crocs/

# Faster, fatter dev build: keeps debug symbols + all 206 grammars so
# `go test` and editor tooling are fully wired.
build-dev:
	CGO_ENABLED=0 go build -o bin/crocs ./cmd/crocs/

# Copy the release binary into ~/.local/bin/.
install: build
	mkdir -p $$HOME/.local/bin
	cp -f bin/crocs $$HOME/.local/bin/crocs

test:
	CGO_ENABLED=0 go test ./...

vet:
	CGO_ENABLED=0 go vet ./...

clean:
	rm -rf bin/

# Build for the canonical release targets so we catch any CGO sneak-in
# early.
cross:
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -tags '$(TAGS)' -ldflags="$(LDFLAGS)" -trimpath -o bin/crocs-darwin-arm64  ./cmd/crocs/
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -tags '$(TAGS)' -ldflags="$(LDFLAGS)" -trimpath -o bin/crocs-linux-amd64   ./cmd/crocs/
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -tags '$(TAGS)' -ldflags="$(LDFLAGS)" -trimpath -o bin/crocs-linux-arm64   ./cmd/crocs/
