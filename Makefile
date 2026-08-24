# The commands used in local development. CI runs the same set of checks (see
# .github/workflows/ci.yml), so running `make check` before opening a PR saves
# a round of red.
.PHONY: all build test test-cover race bench lint vet fmt fmt-check examples check clean

# The default target: run the full set of checks.
all: check

build:
	go build ./...

test:
	go test ./...

# Coverage is measured over the kernel and the three rules packages under
# example/, and nothing else.
#
# Left out: enginetest (a harness for rules packages, no tests of its own and
# it should not have any -- it is driven from example/, whose own tests already
# count) and example/ (those are users, not subjects; counting them only
# dilutes the number).
test-cover:
	go test -coverpkg=github.com/Zereker/hiddenrole,github.com/Zereker/hiddenrole/example/werewolf,github.com/Zereker/hiddenrole/example/missions,github.com/Zereker/hiddenrole/example/onenight \
		-coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

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

# Run each program under example/werewolf/ once, only to confirm the public API
# still holds them up: they are its first real users.
examples:
	go run ./example/werewolf/demo > /dev/null
	printf 'run\nquit\n' | go run ./example/werewolf/cli > /dev/null
	go run ./example/werewolf/extension > /dev/null

check: build vet fmt-check lint test race examples

clean:
	go clean
