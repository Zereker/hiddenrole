# The commands used in local development. CI runs the same set of checks (see
# .github/workflows/ci.yml), so running `make check` before opening a PR saves
# a round of red.
.PHONY: all build test test-cover race bench lint vet fmt fmt-check check clean

# The default target: run the full set of checks.
all: check

build:
	go build ./...

test:
	go test ./...

# Coverage is measured over the kernel package alone (`.`, not `./...`), which
# leaves out the enginetest sub-package: that is a test harness for rules
# packages, it has no tests of its own and should not have any -- the code that
# drives it lives in another module, and cross-module coverage cannot be
# measured anyway. Counting it drags the number from 87.8% down to 76.9%, and
# those 11 points are an artefact of how it is measured, not code that is
# untested.
test-cover:
	go test -coverprofile=coverage.out . && go tool cover -func=coverage.out | tail -1

race:
	go test -race ./...

# Run each benchmark once, only to confirm it still works; timings are not
# compared.
bench:
	go test -bench=. -benchtime=1x -run '^$$' ./...

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi

check: build vet fmt-check lint test race

clean:
	go clean
