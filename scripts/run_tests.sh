#!/bin/bash
set -euo pipefail

echo "=== Tabulum Kernel Tests ==="
echo ""

echo "--- Formatting check ---"
gofmt -l ./... | tee /dev/stderr | (! read) || { echo "FAIL: files need formatting"; exit 1; }
echo "PASS"
echo ""

echo "--- Vet ---"
go vet ./...
echo "PASS"
echo ""

echo "--- Unit tests ---"
go test ./internal/... -v -race -count=1
echo ""

echo "--- Integration tests ---"
go test ./test/... -v -race -count=1 -tags=integration
echo ""

echo "=== All tests passed ==="
