#!/bin/bash
# Ankra CLI Installation Script
# Professional installer for the Ankra platform CLI tool

set -e

# Colors and formatting
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly PURPLE='\033[0;35m'
readonly CYAN='\033[0;36m'
readonly WHITE='\033[1;37m'
readonly NC='\033[0m' # No Color
readonly BOLD='\033[1m'

# Configuration
readonly BINARY_NAME="ankra"
readonly INSTALL_DIR="/usr/local/bin"
readonly DOWNLOAD_URL="https://artifact.infra.ankra.cloud/repository/ankra-install-public/cli/ankra"

# Helper functions
print_header() {
    echo -e "${CYAN}${BOLD}"
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║                    ANKRA CLI INSTALLER                       ║"
    echo "║                   https://www.ankra.io                       ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

print_step() {
    echo -e "${BLUE}${BOLD}▶${NC} $1"
}

print_success() {
    echo -e "${GREEN}${BOLD}✓${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}${BOLD}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}${BOLD}✗${NC} $1"
}

print_info() {
    echo -e "${PURPLE}${BOLD}ℹ${NC} $1"
}

# Main installation function
main() {
    print_header

    print_step "Starting Ankra CLI installation..."
    echo

    # Check if already installed
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

    # Download binary
    print_step "Downloading Ankra CLI binary..."
    if ! curl -sSL "$DOWNLOAD_URL" -o "$BINARY_NAME"; then
        print_error "Failed to download binary from artifact repository"
        print_info "Please check your internet connection and try again"
        exit 1
    fi
    print_success "Binary downloaded successfully"

    # Verify download
    if [[ ! -f "$BINARY_NAME" ]] || [[ ! -s "$BINARY_NAME" ]]; then
        print_error "Downloaded file is empty or corrupted"
        exit 1
    fi

    # Make executable
    print_step "Setting executable permissions..."
    chmod +x "$BINARY_NAME"
    print_success "Permissions set"

    # Install binary
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

    # Verify installation
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

    # Configuration instructions
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
        echo -e "   ${GRAY}echo 'export ANKRA_BASE_URL=https://platform.ankra.app' >> ~/.zshrc${NC}"
    else
        echo -e "   ${GRAY}echo 'export ANKRA_API_TOKEN=your-token' >> ~/.bashrc${NC}"
        echo -e "   ${GRAY}echo 'export ANKRA_BASE_URL=https://platform.ankra.app' >> ~/.bashrc${NC}"
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
    echo -e "${WHITE}6.${NC} ${BOLD}Deploy addons:${NC}"
    echo -e "   ${CYAN}ankra apply fluent-bit${NC}"
    echo -e "   ${CYAN}ankra apply cert-manager${NC}"
    echo
    echo -e "${WHITE}7.${NC} ${BOLD}Get started:${NC}"
    echo -e "   ${CYAN}ankra --help${NC}"
    echo
    echo -e "${CYAN}─────────────────────────────────────────────────────────────${NC}"
    echo -e "${PURPLE}${BOLD}📚 Documentation:${NC} ${CYAN}https://docs.ankra.io${NC}"
    echo -e "${PURPLE}${BOLD}🆘 Support:${NC}       ${CYAN}hello@ankra.io${NC}"
    echo
}

# Cleanup function
cleanup() {
    if [[ -f "$BINARY_NAME" ]]; then
        rm -f "$BINARY_NAME"
    fi
}

# Set trap for cleanup
trap cleanup EXIT

# Run main installation
main "$@"

