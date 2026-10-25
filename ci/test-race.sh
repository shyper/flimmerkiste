#!/bin/sh
# The Go tests with the race detector (needs cgo and a C compiler). Without the
# end-to-end test, and -short leaves out the slowest picture tests: the detector
# slows rendering down many times.
set -eu
CGO_ENABLED=1 go test -race -short -timeout 30m -p 2 ./...
