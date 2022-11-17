#!/bin/sh

# This script is used to generate test coverage reports for the
# go project. It is intended to be run from the root of the project
# directory.

echo "Running tests and generating coverage report..."
go test -coverprofile=coverage.out

echo "Opening coverage report..."
go tool cover -html=coverage.out

echo "Done."