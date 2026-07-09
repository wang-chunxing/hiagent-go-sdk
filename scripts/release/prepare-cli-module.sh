#!/usr/bin/env bash
#
# Prepare Go dependencies for GoReleaser.
#
# The CLI is built from the repository root module so `go install
# github.com/volcengine/hiagent-go-sdk/cmd/hibot@latest` works without
# release-time go.mod rewriting.
set -euo pipefail

echo "[prepare-cli-module] downloading root module dependencies"
go mod download
