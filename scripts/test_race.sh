#!/bin/bash
# Run Go tests with the race detector enabled
# Usage: bash scripts/test_race.sh

set -e

export CGO_ENABLED=1

SCRIPT_DUR="$(dirname "$(realpath "$0")")"
GO_MOD_DIR="$(dirname "$SCRIPT_DUR")"

cd "$GO_MOD_DIR"
if [ ! -f "go.mod" ]; then
  echo "go.mod not found. Please run this script from within a Go module."
  exit 1
fi

echo "Running Go tests with race detector..."
go test -race ./...
