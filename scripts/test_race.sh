#!/bin/bash
# Run Go tests with the race detector enabled
# Usage: bash scripts/test_race.sh

set -e

export CGO_ENABLED=1

echo "Running Go tests with race detector..."
go test -race ./...
