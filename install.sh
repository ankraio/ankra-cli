#!/bin/bash

set -e

readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly PURPLE='\033[0;35m'
readonly CYAN='\033[0;36m'
readonly WHITE='\033[1;37m'
readonly NC='\033[0m'
readonly BOLD='\033[1m'
readonly GRAY='\033[0;37m'

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
echo -e "${CYAN}${BOLD}Detected OS: $OS, ARCH: $ARCH${NC}"
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)
        echo -e "${RED}${BOLD}✗ Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac


BASE_URL="https://github.com/ankraio/ankra-cli/releases/latest/download"
if [[ "$OS" == "darwin" ]]; then
    if [[ "$ARCH" == "arm64" ]]; then
        DOWNLOAD_URL="$BASE_URL/ankra-cli-darwin-arm64"
    elif [[ "$ARCH" == "amd64" ]]; then
        DOWNLOAD_URL="$BASE_URL/ankra-cli-darwin-amd64"
    else
        echo -e "${RED}${BOLD}✗ Unsupported architecture: $ARCH${NC}"
        exit 1
    fi
elif [[ "$OS" == "linux" ]]; then
    if [[ "$ARCH" == "arm64" ]]; then
        DOWNLOAD_URL="$BASE_URL/ankra-cli-linux-arm64"
    elif [[ "$ARCH" == "amd64" ]]; then
        DOWNLOAD_URL="$BASE_URL/ankra-cli-linux-amd64"
    else
        echo -e "${RED}${BOLD}✗ Unsupported architecture: $ARCH${NC}"
        exit 1
    fi
else
    echo -e "${RED}${BOLD}✗ Unsupported OS: $OS${NC}"
    exit 1
fi

readonly BINARY_NAME="ankra"
readonly INSTALL_DIR="/usr/local/bin"

print_header() {
    echo -e "${CYAN}${BOLD}"
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║                    ANKRA CLI INSTALLER                       ║"
    echo "║                   https://www.ankra.io                       ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

print_step() { echo -e "${BLUE}${BOLD}▶${NC} $1"; }
print_success() { echo -e "${GREEN}${BOLD}✓${NC} $1"; }
print_warning() { echo -e "${YELLOW}${BOLD}⚠${NC} $1"; }
print_error() { echo -e "${RED}${BOLD}✗${NC} $1"; }
print_info() { echo -e "${PURPLE}${BOLD}ℹ${NC} $1"; }

main() {
    print_header
    print_step "Starting Ankra CLI installation..."
    echo
    if command -v "$BINARY_NAME" &> /dev/null; then
        current_version=$("$BINARY_NAME" --version 2>/dev/null | head -1 || echo "installed")
        print_warning "Ankra CLI is already installed ($current_version)"
        echo -n "Do you want to reinstall? [y/N]: "
        read -r response
        if [[ ! "$response" =~ ^[Yy]$ ]]; then
            echo
            print_info "Installation cancelled."
            exit 0
        fi
        echo
    fi
print_step "Downloading Ankra CLI binary for $OS/$ARCH..."
echo -e "${CYAN}Downloading from: $DOWNLOAD_URL${NC}"
http_status=$(curl -s -w "%{http_code}" -L "$DOWNLOAD_URL" -o "$BINARY_NAME")
if [[ "$http_status" != "200" ]]; then
    print_error "Download failed (HTTP status: $http_status)"
    print_info "The binary may not exist for this release or platform."
    print_info "URL: $DOWNLOAD_URL"
    exit 1
fi
print_success "Binary downloaded successfully"
if [[ ! -f "$BINARY_NAME" ]] || [[ ! -s "$BINARY_NAME" ]]; then
    print_error "Downloaded file is empty or corrupted"
    exit 1
fi
    print_step "Setting executable permissions..."
    chmod +x "$BINARY_NAME"
    print_success "Permissions set"
    if [[ "$OS" == "darwin" ]]; then
        print_step "Removing quarantine attribute (macOS only)..."
        if command -v xattr >/dev/null 2>&1; then
            xattr -d com.apple.quarantine "$BINARY_NAME" 2>/dev/null || true
            print_success "Quarantine attribute removed"
        else
            print_warning "xattr command not found, skipping quarantine removal"
        fi
    fi
    print_step "Installing to $INSTALL_DIR..."
    if [[ ! -w "$INSTALL_DIR" ]]; then
        print_warning "Administrator privileges required for installation"
        if ! sudo mv "$BINARY_NAME" "$INSTALL_DIR/"; then
            print_error "Failed to install binary"
            exit 1
        fi
    else
        if ! mv "$BINARY_NAME" "$INSTALL_DIR/"; then
            print_error "Failed to install binary"
            exit 1
        fi
    fi
    print_success "Ankra CLI installed to $INSTALL_DIR/$BINARY_NAME"
    print_step "Verifying installation..."
    if command -v "$BINARY_NAME" &> /dev/null; then
        version=$("$BINARY_NAME" --version 2>/dev/null || "$BINARY_NAME" version 2>/dev/null || echo "installed")
        print_success "Installation verified (version: $version)"
    else
        print_error "Installation verification failed"
        print_info "You may need to restart your terminal or add $INSTALL_DIR to your PATH"
        exit 1
    fi
    echo
    print_success "Ankra CLI installation completed successfully!"
    echo
    echo -e "${WHITE}${BOLD}🚀 NEXT STEPS${NC}"
    echo -e "${CYAN}─────────────────────────────────────────────────────────────${NC}"
    echo
    echo -e "${WHITE}1.${NC} ${BOLD}Configure Authentication:${NC}"
    echo -e "   ${YELLOW}export ANKRA_API_TOKEN=your-api-token${NC}"
    echo
    echo -e "${WHITE}2.${NC} ${BOLD}Set Platform URL (optional):${NC}"
    echo -e "   ${YELLOW}export ANKRA_BASE_URL=https://platform.ankra.app${NC}"
    echo
    echo -e "${WHITE}3.${NC} ${BOLD}Add to shell profile for persistence:${NC}"
    if [[ "$SHELL" == *"zsh"* ]]; then
        echo -e "   ${GRAY}echo 'export ANKRA_API_TOKEN=your-token' >> ~/.zshrc${NC}"
    else
        echo -e "   ${GRAY}echo 'export ANKRA_API_TOKEN=your-token' >> ~/.bashrc${NC}"
    fi
    echo
    echo -e "${WHITE}4.${NC} ${BOLD}Reload your shell profile:${NC}"
    if [[ "$SHELL" == *"zsh"* ]]; then
        echo -e "   ${YELLOW}source ~/.zshrc${NC}"
    else
        echo -e "   ${YELLOW}source ~/.bashrc${NC}"
    fi
    echo
    echo -e "${WHITE}5.${NC} ${BOLD}Select a cluster:${NC}"
    echo -e "   ${CYAN}ankra cluster select${NC}"
    echo
    echo -e "${WHITE}7.${NC} ${BOLD}Get started:${NC}"
    echo -e "   ${CYAN}ankra --help${NC}"
    echo
    echo -e "${CYAN}─────────────────────────────────────────────────────────────${NC}"
    echo -e "${PURPLE}${BOLD}📚 Documentation:${NC} ${CYAN}https://docs.ankra.io${NC}"
    echo -e "${PURPLE}${BOLD}🆘 Support:${NC}       ${CYAN}hello@ankra.io${NC}"
    echo
}

cleanup() {
    if [[ -f "$BINARY_NAME" ]]; then
        rm -f "$BINARY_NAME"
    fi
}
trap cleanup EXIT
main "$@"

