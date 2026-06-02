.PHONY: build build-dev install test vet clean cross prepare

# The five grammar_subset_<lang> tags pick exactly the languages crocs
# parses (PLAN.md §1). Together with `grammar_subset`, this drops the
# gotreesitter binary footprint from ~30MB (all 206 grammars) to ~6MB
# (just these five). See [[reference-gotreesitter-api]].
TAGS    := grammar_subset grammar_subset_python grammar_subset_typescript grammar_subset_tsx grammar_subset_java grammar_subset_go
LDFLAGS := -s -w

# Stage embedded assets that live outside the importing package — go:embed
# refuses to traverse ".." so the canonical SKILL.md at skills/crocs/ is
# copied into internal/skill/ where it can be embedded. Idempotent.
prepare:
	cp -f skills/crocs/SKILL.md internal/skill/SKILL.md

# Canonical release build: stripped, trimmed, subset-embedded grammars,
# CGO disabled. This is the binary `make install` puts on $PATH.
build: prepare
	CGO_ENABLED=0 go build \
		-tags '$(TAGS)' \
		-ldflags="$(LDFLAGS)" \
		-trimpath \
		-o bin/crocs ./cmd/crocs/

# Faster, fatter dev build: keeps debug symbols + all 206 grammars so
# `go test` and editor tooling are fully wired.
build-dev: prepare
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
# early. Matches PLAN.md §8.
cross: prepare
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -tags '$(TAGS)' -ldflags="$(LDFLAGS)" -trimpath -o bin/crocs-darwin-arm64  ./cmd/crocs/
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -tags '$(TAGS)' -ldflags="$(LDFLAGS)" -trimpath -o bin/crocs-linux-amd64   ./cmd/crocs/
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -tags '$(TAGS)' -ldflags="$(LDFLAGS)" -trimpath -o bin/crocs-linux-arm64   ./cmd/crocs/
