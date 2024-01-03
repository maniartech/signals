#!/bin/sh

# This script is used to generate test coverage reports for the
# go project. It is intended to be run from the root of the project
# directory.

# filename is a timestamped filename for the coverage report in the
# .coverages directory.
filename=".coverages/coverage-$(date +%s).out"

# If the .coverages directory does not exist, create it.
if [ ! -d ".coverages" ]; then
    mkdir .coverages
fi

echo "Running tests and generating coverage report..."
go test -coverprofile=$filename

echo "Opening coverage report..."
go tool cover -html=$filename

echo "Done."
