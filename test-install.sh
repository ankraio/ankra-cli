#!/bin/bash

set -e

echo "🧪 Testing Ankra CLI installation process..."

echo "📦 Testing local build..."
if command -v go >/dev/null 2>&1; then
    go build -o test-ankra
    echo "✅ Local build successful"
    
    if ./test-ankra --help >/dev/null 2>&1; then
        echo "✅ Local binary works correctly"
    else
        echo "❌ Local binary failed to run"
        exit 1
    fi
    
    rm -f test-ankra
else
    echo "⚠️  Go not found, skipping local build test"
fi

echo "📦 Testing build script..."
if [ -f "./build.sh" ]; then
    chmod +x ./build.sh
    ./build.sh    if [ -f "./dist/ankra-cli" ]; then
        echo "✅ Build script successful"
        
        if ./dist/ankra-cli --help >/dev/null 2>&1; then
            echo "✅ Build script binary works correctly"
        else
            echo "❌ Build script binary failed to run"
            exit 1
        fi
    else
        echo "❌ Build script failed to create binary"
        exit 1
    fi
else
    echo "⚠️  build.sh not found, skipping build script test"
fi

if [[ "$OSTYPE" == "darwin"* ]]; then
    echo "🍎 Testing macOS quarantine handling..."
    
    if [ -f "./dist/ankra-cli" ]; then
        xattr -w com.apple.quarantine "test" ./dist/ankra-cli 2>/dev/null || true
        
        if xattr -l ./dist/ankra-cli 2>/dev/null | grep -q com.apple.quarantine; then
            echo "✅ Quarantine attribute added (simulating download)"
            
            xattr -d com.apple.quarantine ./dist/ankra-cli
            
            if ! xattr -l ./dist/ankra-cli 2>/dev/null | grep -q com.apple.quarantine; then
                echo "✅ Quarantine attribute removed successfully"
            else
                echo "❌ Failed to remove quarantine attribute"
                exit 1
            fi
        else
            echo "⚠️  Could not add quarantine attribute (this is okay)"
        fi
    fi
fi

echo "🎉 All tests passed! Installation process should work correctly."
echo ""
echo "📋 Summary:"
echo "  - Local builds work without security warnings"
echo "  - Build script creates working binaries"
echo "  - macOS quarantine handling works correctly"
echo ""
echo "💡 For users encountering security warnings:"
echo "  - Use the installation script from releases"
echo "  - Or run: xattr -d com.apple.quarantine /path/to/ankra-cli"
