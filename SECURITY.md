# Security and Code Signing

## Recommended Installation Methods

### Method 1: Use the Installation Script (Easiest)
Download and run the installation script that automatically handles the security bypass:

```bash
bash <(curl -sL https://github.com/ankraio/ankra-cli/releases/latest/download/install.sh)
```

### Method 2: Manual Installation with Security Bypass
1. Download the binary for your architecture:
   ```bash
   # For Intel Macs
   curl -sSL https://github.com/ankraio/ankra-cli/releases/latest/download/ankra-cli-darwin-amd64 -o ankra

   # For Apple Silicon Macs
   curl -sSL https://github.com/ankraio/ankra-cli/releases/latest/download/ankra-cli-darwin-arm64 -o ankra
   ```

2. Remove the quarantine attribute:
   ```bash
   xattr -d com.apple.quarantine ankra
   ```

3. Make it executable and install:
   ```bash
   chmod +x ankra
   sudo mv ankra /usr/local/bin/
   ```

## Future Plans

We plan to implement proper code signing and notarization once we have Apple Developer Program membership. Until then, the methods above are safe to use.
