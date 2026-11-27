BINARY  := bin/driftwatch
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE   ?= driftwatch:$(VERSION)

.PHONY: all build test test-e2e cover lint fmt validate fixtures golden example docker clean

all: lint test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/driftwatch

test:
	go test ./...

# Runs the end-to-end test against a real terraform binary. Needs network
# access to download the null, random and local providers.
test-e2e:
	DRIFTWATCH_E2E=1 go test ./internal/cli -run EndToEnd -v

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)
	go vet ./...
	terraform fmt -check -recursive testdata/workspaces

fmt:
	gofmt -w .
	terraform fmt -recursive testdata/workspaces

validate:
	for ws in drift sensitive; do 		(cd testdata/workspaces/$$ws && terraform init -input=false -backend=false >/dev/null && terraform validate) || exit 1; 	done

# Regenerates testdata/plans/*.json from the real Terraform workspace.
fixtures:
	./scripts/gen-fixtures.sh

# Rewrites testdata/golden after an intentional report change.
golden:
	go test ./internal/report -update

example: build
	$(BINARY) check --plan-json testdata/plans/drift.json --workspace payments-prod \
		--ignore-file examples/driftignore --markdown-out reports/drift.md --json-out reports/drift.json

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

clean:
	go clean
	rm -rf bin dist reports coverage.out
