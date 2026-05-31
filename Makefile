.PHONY: build test lint generate verify-skills

# Path to the canonical ankra-skills project (monorepo sibling). In the split
# ankra-cli repo this does not exist and the vendored embedded copy is canonical.
SKILLS_SYNC := ../ankra-skills/scripts/sync-skills.sh
SKILLS_CATALOG := ../ankra-skills/scripts/build-catalog.sh
EMBEDDED_SKILLS := internal/skills/embedded/skills

build:
	go build ./...

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run

# generate vendors the canonical skills into the CLI for //go:embed and
# regenerates the ankra-skills catalog. No-op when the sibling is absent.
generate:
	@if [ -f $(SKILLS_SYNC) ]; then \
		bash $(SKILLS_SYNC); \
		bash $(SKILLS_CATALOG); \
	else \
		echo "ankra-skills source not present; embedded copy is canonical, skipping generate"; \
	fi

# verify-skills fails if the vendored embedded skills are out of sync with the
# canonical ankra-skills/skills. Trivially passes in the split repo.
verify-skills:
	@if [ -f $(SKILLS_SYNC) ]; then \
		bash $(SKILLS_SYNC); \
		if [ -n "$$(git status --porcelain -- $(EMBEDDED_SKILLS))" ]; then \
			echo "embedded skills are out of sync with ankra-skills; run 'make generate' and commit" >&2; \
			git status --porcelain -- $(EMBEDDED_SKILLS) >&2; \
			exit 1; \
		fi; \
		echo "embedded skills in sync"; \
	else \
		echo "ankra-skills source not present; embedded copy is canonical, skipping verify"; \
	fi
