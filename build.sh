#!/bin/bash

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

BINARY_NAME="ankra-cli"
OUTPUT_DIR="./dist"
GOOS=${GOOS:-$(go env GOOS)}
GOARCH=${GOARCH:-$(go env GOARCH)}

mkdir -p "$OUTPUT_DIR"

echo -e "${GREEN}Building ankra-cli for ${GOOS}/${GOARCH}...${NC}"

if [ "$GOOS" = "windows" ]; then
    BINARY_PATH="$OUTPUT_DIR/${BINARY_NAME}.exe"
else
    BINARY_PATH="$OUTPUT_DIR/${BINARY_NAME}"
fi

CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags="-w -s" -o "$BINARY_PATH"

echo -e "${GREEN}Binary built: $BINARY_PATH${NC}"

if [ "$GOOS" = "darwin" ]; then
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

    echo -e "${GREEN}macOS binary ready: $BINARY_PATH${NC}"
    echo -e "${YELLOW}If you get a security warning, right-click and select 'Open'${NC}"
fi

echo -e "${GREEN}Build complete!${NC}"

if [ "$1" = "--install" ]; then
    echo -e "${YELLOW}Installing to /usr/local/bin...${NC}"
    if [ "$GOOS" = "windows" ]; then
        echo -e "${RED}Cannot install Windows binary on non-Windows system${NC}"
        exit 1
    fi

    sudo cp "$BINARY_PATH" "/usr/local/bin/${BINARY_NAME}"
    echo -e "${GREEN}Installed to /usr/local/bin/${BINARY_NAME}${NC}"
fi
