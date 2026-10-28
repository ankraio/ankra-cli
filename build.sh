#!/usr/bin/env bash

# Local build script for ankra-cli. Produces dist/ankra (or dist/ankra.exe
# on Windows) using the same flags as the release pipeline so that local
# binaries match what users will receive from the GitHub Releases page.
#
# Pass --install to copy the freshly built binary to /usr/local/bin/ankra.

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

readonly BINARY_NAME="ankra"
readonly OUTPUT_DIR="./dist"
GOOS=${GOOS:-$(go env GOOS)}
GOARCH=${GOARCH:-$(go env GOARCH)}

VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null || echo "dev")}

mkdir -p "$OUTPUT_DIR"

echo -e "${GREEN}Building ${BINARY_NAME} ${VERSION} for ${GOOS}/${GOARCH}...${NC}"

BINARY_PATH="$OUTPUT_DIR/${BINARY_NAME}"
if [[ "$GOOS" == "windows" ]]; then
    BINARY_PATH="${BINARY_PATH}.exe"
fi

CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags="-w -s -X main.version=${VERSION}" -o "$BINARY_PATH"

echo -e "${GREEN}Binary built: ${BINARY_PATH}${NC}"

if [[ "$GOOS" == "darwin" ]]; then
    echo -e "${YELLOW}Handling macOS-specific tasks...${NC}"

    if xattr -l "$BINARY_PATH" 2>/dev/null | grep -q com.apple.quarantine; then
        echo -e "${YELLOW}Removing quarantine attribute...${NC}"
        xattr -d com.apple.quarantine "$BINARY_PATH"
    fi

    chmod +x "$BINARY_PATH"

    if security find-identity -v -p codesigning | grep -q "Developer ID Application"; then
        echo -e "${YELLOW}Found Developer ID certificate, attempting to sign...${NC}"
        SIGNING_IDENTITY=$(security find-identity -v -p codesigning | grep "Developer ID Application" | head -1 | sed 's/.*"\(.*\)".*/\1/')

        if codesign --force --options runtime --sign "$SIGNING_IDENTITY" "$BINARY_PATH" 2>/dev/null; then
            echo -e "${GREEN}Binary signed successfully${NC}"
        else
            echo -e "${YELLOW}Warning: Could not sign binary (this is okay for local development)${NC}"
        fi
    else
        echo -e "${YELLOW}No Developer ID certificate found - binary will not be signed${NC}"
        echo -e "${YELLOW}Users may see a security warning when running the binary${NC}"
    fi
fi

echo -e "${GREEN}Build complete!${NC}"

if [[ "${1:-}" == "--install" ]]; then
    if [[ "$GOOS" == "windows" ]]; then
        echo -e "${RED}Cannot install Windows binary on a non-Windows system${NC}"
        exit 1
    fi
    echo -e "${YELLOW}Installing to /usr/local/bin/${BINARY_NAME}...${NC}"
    sudo install -m 0755 "$BINARY_PATH" "/usr/local/bin/${BINARY_NAME}"
    echo -e "${GREEN}Installed to /usr/local/bin/${BINARY_NAME}${NC}"
fi
