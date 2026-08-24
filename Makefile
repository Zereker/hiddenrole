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

# Coverage over both packages. It used to be measured over `.` alone, on the
# grounds that enginetest "is a test harness for rules packages, it has no
# tests of its own and should not have any" -- which was true, and was the
# problem: the strongest verification this project has had no caller here, so
# the kernel's own CI never ran it.
test-cover:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

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
