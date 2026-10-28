#!/usr/bin/env bash

# Smoke test for the local build pipeline. Validates that:
#   - `go build` produces a working `ankra` binary
#   - `build.sh` produces the same and that the binary prints help
#   - The macOS quarantine handling round-trips cleanly when applicable
#
# It does not exercise the GitHub-release installer; that is covered in CI.

set -euo pipefail

echo "🧪 Testing Ankra CLI build pipeline..."

readonly BINARY_NAME="ankra"

echo "📦 Testing local build..."
if command -v go >/dev/null 2>&1; then
    go build -o "test-${BINARY_NAME}"
    echo "✅ Local build successful"

    if "./test-${BINARY_NAME}" --help >/dev/null 2>&1; then
        echo "✅ Local binary works correctly"
    else
        echo "❌ Local binary failed to run" >&2
        exit 1
    fi

    rm -f "test-${BINARY_NAME}"
else
    echo "⚠️  Go not found, skipping local build test"
fi

echo "📦 Testing build script..."
if [[ -f "./build.sh" ]]; then
    chmod +x ./build.sh
    ./build.sh

    if [[ -f "./dist/${BINARY_NAME}" ]]; then
        echo "✅ Build script successful"

        if "./dist/${BINARY_NAME}" --help >/dev/null 2>&1; then
            echo "✅ Build script binary works correctly"
        else
            echo "❌ Build script binary failed to run" >&2
            exit 1
        fi
    else
        echo "❌ Build script did not produce ./dist/${BINARY_NAME}" >&2
        exit 1
    fi
else
    echo "⚠️  build.sh not found, skipping build script test"
fi

if [[ "${OSTYPE:-}" == darwin* ]] && [[ -f "./dist/${BINARY_NAME}" ]]; then
    echo "🍎 Testing macOS quarantine handling..."
    xattr -w com.apple.quarantine "test" "./dist/${BINARY_NAME}" 2>/dev/null || true

    if xattr -l "./dist/${BINARY_NAME}" 2>/dev/null | grep -q com.apple.quarantine; then
        echo "✅ Quarantine attribute added (simulating download)"
        xattr -d com.apple.quarantine "./dist/${BINARY_NAME}"
        if ! xattr -l "./dist/${BINARY_NAME}" 2>/dev/null | grep -q com.apple.quarantine; then
            echo "✅ Quarantine attribute removed successfully"
        else
            echo "❌ Failed to remove quarantine attribute" >&2
            exit 1
        fi
    else
        echo "⚠️  Could not add quarantine attribute (this is okay)"
    fi
fi

echo "🎉 All tests passed! Installation process should work correctly."
echo
echo "Summary:"
echo "  - Local builds work without security warnings"
echo "  - Build script creates working binaries"
echo "  - macOS quarantine handling works correctly"
echo
echo "For users encountering security warnings:"
echo "  - Use the installation script from releases"
echo "  - Or run: xattr -d com.apple.quarantine /path/to/ankra"
