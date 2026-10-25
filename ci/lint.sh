#!/bin/sh
# gofmt and go vet, as GitHub and GitLab run them.
set -eu
unformatted=$(gofmt -l cmd internal web)
if [ -n "$unformatted" ]; then
	echo "Please run gofmt:"
	echo "$unformatted"
	exit 1
fi
go vet ./...
