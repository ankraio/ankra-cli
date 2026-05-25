#!/usr/bin/env bash

# Ankra CLI installer.
#
# Behaviour:
#   - Detect OS and architecture
#   - Download the matching ankra-cli binary into a private temp directory
#   - Download the matching .sha256 checksum and verify the binary against it
#   - On macOS, strip the quarantine attribute so the binary can run
#   - Install to /usr/local/bin/ankra (with sudo if needed)
#   - Clean up the temp directory on exit, no matter what
#
# The installer never deletes any pre-existing file in the current
# working directory.

set -euo pipefail

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

readonly BINARY_NAME="ankra"
readonly INSTALL_DIR="/usr/local/bin"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)
        echo -e "${RED}${BOLD}✗ Unsupported architecture: ${ARCH}${NC}" >&2
        exit 1
        ;;
esac

print_step()    { echo -e "${BLUE}${BOLD}▶${NC} $1"; }
print_success() { echo -e "${GREEN}${BOLD}✓${NC} $1"; }
print_warning() { echo -e "${YELLOW}${BOLD}⚠${NC} $1"; }
print_error()   { echo -e "${RED}${BOLD}✗${NC} $1" >&2; }
print_info()    { echo -e "${PURPLE}${BOLD}ℹ${NC} $1"; }

set_download_url() {
    if [[ "$VERSION" == "latest" ]]; then
        BASE_URL="https://github.com/ankraio/ankra-cli/releases/latest/download"
    else
        BASE_URL="https://github.com/ankraio/ankra-cli/releases/download/$VERSION"
    fi

    case "$OS" in
        darwin|linux)
            BINARY_ASSET="ankra-cli-${OS}-${ARCH}"
            ;;
        *)
            print_error "Unsupported OS: $OS"
            exit 1
            ;;
    esac

    DOWNLOAD_URL="${BASE_URL}/${BINARY_ASSET}"
    CHECKSUM_URL="${DOWNLOAD_URL}.sha256"
}

print_header() {
    echo -e "${CYAN}${BOLD}"
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║                    ANKRA CLI INSTALLER                       ║"
    echo "║                   https://www.ankra.io                       ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

verify_checksum() {
    local binary_path="$1"
    local checksum_path="$2"

    if [[ ! -s "$checksum_path" ]]; then
        print_warning "No checksum file available - skipping verification."
        print_warning "Consider pinning to a tagged release that ships checksums."
        return 0
    fi

    local expected
    expected=$(awk '{print $1}' "$checksum_path" | head -n 1)
    if [[ -z "$expected" ]]; then
        print_error "Checksum file is empty"
        return 1
    fi

    local actual
    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$binary_path" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "$binary_path" | awk '{print $1}')
    else
        print_error "No sha256 tool found (need sha256sum or shasum)"
        return 1
    fi

    if [[ "$expected" != "$actual" ]]; then
        print_error "Checksum mismatch."
        print_error "  expected: ${expected}"
        print_error "  actual:   ${actual}"
        return 1
    fi

    print_success "Checksum verified."
    return 0
}

main() {
    VERSION="latest"
    if [[ "${1:-}" == "--version" || "${1:-}" == "-v" ]]; then
        if [[ -n "${2:-}" ]]; then
            VERSION="$2"
        else
            print_error "No version specified after $1"
            exit 1
        fi
    fi

    set_download_url
    print_header
    print_step "Starting Ankra CLI installation..."
    echo -e "${CYAN}Detected OS: ${OS}, ARCH: ${ARCH}${NC}"
    echo

    if command -v "$BINARY_NAME" >/dev/null 2>&1; then
        local current_version
        current_version=$("$BINARY_NAME" --version 2>/dev/null | head -1 || echo "installed")
        local target_version
        if [[ "$VERSION" == "latest" ]]; then
            if command -v curl >/dev/null 2>&1; then
                target_version=$(curl -fsSL https://api.github.com/repos/ankraio/ankra-cli/releases/latest \
                    | grep '"tag_name"' | cut -d'"' -f4 || echo "latest")
            fi
            target_version="${target_version:-latest}"
        else
            target_version="$VERSION"
        fi

        print_warning "Ankra CLI is already installed (${current_version})"
        print_info "Target version: ${target_version}"
        printf "Do you want to reinstall? [y/N]: "
        read -r response
        if [[ ! "$response" =~ ^[Yy]$ ]]; then
            echo
            print_info "Installation cancelled."
            exit 0
        fi
        echo
    fi

    local workdir
    workdir=$(mktemp -d -t ankra-install.XXXXXX)
    # shellcheck disable=SC2064
    trap "rm -rf '$workdir'" EXIT
    local binary_path="${workdir}/${BINARY_NAME}"
    local checksum_path="${workdir}/${BINARY_NAME}.sha256"

    print_step "Downloading Ankra CLI binary for ${OS}/${ARCH}..."
    echo -e "${CYAN}From: ${DOWNLOAD_URL}${NC}"
    local http_status
    http_status=$(curl -fsSL -w "%{http_code}" -o "$binary_path" "$DOWNLOAD_URL")
    if [[ "$http_status" != "200" ]]; then
        print_error "Download failed (HTTP status: ${http_status})"
        print_info "URL: ${DOWNLOAD_URL}"
        exit 1
    fi
    if [[ ! -s "$binary_path" ]]; then
        print_error "Downloaded binary is empty or corrupted"
        exit 1
    fi
    print_success "Binary downloaded."

    print_step "Downloading checksum..."
    if ! curl -fsSL -o "$checksum_path" "$CHECKSUM_URL"; then
        # Older releases may not have a published checksum yet; emit a
        # warning but allow the install to proceed so existing users do
        # not lose access. The next release ships checksums and the
        # warning will go away.
        print_warning "Checksum not available at ${CHECKSUM_URL}"
        : >"$checksum_path"
    fi

    print_step "Verifying checksum..."
    if ! verify_checksum "$binary_path" "$checksum_path"; then
        print_error "Refusing to install: checksum verification failed"
        exit 1
    fi

    print_step "Setting executable permissions..."
    chmod +x "$binary_path"
    print_success "Permissions set."

    if [[ "$OS" == "darwin" ]]; then
        print_step "Removing quarantine attribute (macOS only)..."
        if command -v xattr >/dev/null 2>&1; then
            xattr -d com.apple.quarantine "$binary_path" 2>/dev/null || true
            print_success "Quarantine attribute removed."
        else
            print_warning "xattr not found, skipping quarantine removal"
        fi
    fi

    print_step "Installing to ${INSTALL_DIR}..."
    if [[ ! -w "$INSTALL_DIR" ]]; then
        print_warning "Administrator privileges required for installation"
        if ! sudo install -m 0755 "$binary_path" "${INSTALL_DIR}/${BINARY_NAME}"; then
            print_error "Failed to install binary"
            exit 1
        fi
    else
        if ! install -m 0755 "$binary_path" "${INSTALL_DIR}/${BINARY_NAME}"; then
            print_error "Failed to install binary"
            exit 1
        fi
    fi
    print_success "Ankra CLI installed to ${INSTALL_DIR}/${BINARY_NAME}"

    print_step "Verifying installation..."
    if command -v "$BINARY_NAME" >/dev/null 2>&1; then
        local version
        version=$("$BINARY_NAME" --version 2>/dev/null || "$BINARY_NAME" version 2>/dev/null || echo "installed")
        print_success "Installation verified (version: ${version})"
    else
        print_error "Installation verification failed"
        print_info "You may need to restart your terminal or add ${INSTALL_DIR} to your PATH"
        exit 1
    fi

    echo
    print_success "Ankra CLI installation completed successfully!"
    echo
    echo -e "${WHITE}${BOLD}NEXT STEPS${NC}"
    echo -e "${CYAN}─────────────────────────────────────────────────────────────${NC}"
    echo
    echo -e "${WHITE}1.${NC} ${BOLD}Login to Ankra:${NC}"
    echo -e "   ${CYAN}ankra login${NC}"
    echo
    echo -e "${WHITE}2.${NC} ${BOLD}Enable shell completions:${NC}"
    echo -e "   ${CYAN}ankra completion install${NC}"
    echo
    echo -e "${WHITE}3.${NC} ${BOLD}Select a cluster to work with:${NC}"
    echo -e "   ${CYAN}ankra cluster list${NC}"
    echo -e "   ${CYAN}ankra cluster select${NC}"
    echo
    echo -e "${CYAN}─────────────────────────────────────────────────────────────${NC}"
    echo
    echo -e "${WHITE}${BOLD}USEFUL COMMANDS${NC}"
    echo
    echo -e "   ${CYAN}ankra cluster info${NC}            ${GRAY}Show current cluster details${NC}"
    echo -e "   ${CYAN}ankra cluster get pods${NC}        ${GRAY}List pods across namespaces${NC}"
    echo -e "   ${CYAN}ankra org list${NC}                ${GRAY}List your organisations${NC}"
    echo -e "   ${CYAN}ankra chat${NC}                    ${GRAY}Chat with Ankra AI${NC}"
    echo
    echo -e "${CYAN}─────────────────────────────────────────────────────────────${NC}"
    echo -e "${PURPLE}${BOLD}Documentation:${NC} ${CYAN}https://docs.ankra.io${NC}"
    echo -e "${PURPLE}${BOLD}Support:${NC}       ${CYAN}hello@ankra.io${NC}"
    echo
}

main "$@"
