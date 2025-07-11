# Security and Code Signing

## macOS Security Warning

When downloading and running the `ankra-cli` binary on macOS, you may encounter a security warning:

> "Apple cannot verify "ankra" is free of malware that may harm your Mac or compromise your privacy."

This warning appears because the binary is downloaded from the internet and not code-signed by Apple. This is normal for open-source CLI tools.

## Recommended Installation Methods

### Method 1: Use the Installation Script (Easiest)
Download and run the installation script that automatically handles the security bypass:

```bash
# For Intel Macs (amd64)
curl -sSL https://github.com/your-org/ankra-cli/releases/latest/download/install-macos-amd64.sh | bash

# For Apple Silicon Macs (arm64)
curl -sSL https://github.com/your-org/ankra-cli/releases/latest/download/install-macos-arm64.sh | bash
```

### Method 2: Manual Installation with Security Bypass
1. Download the binary for your architecture:
   ```bash
   # For Intel Macs
   curl -sSL https://github.com/your-org/ankra-cli/releases/latest/download/ankra-cli-darwin-amd64 -o ankra

   # For Apple Silicon Macs
   curl -sSL https://github.com/your-org/ankra-cli/releases/latest/download/ankra-cli-darwin-arm64 -o ankra
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

## Alternative Bypass Methods

### Option 1: System Preferences Override
1. Try to run the binary - you'll get the security warning
2. Go to **System Preferences** → **Security & Privacy** → **General**
3. Click **"Allow Anyway"** next to the blocked app message
4. Try running the binary again and click **"Open"** when prompted

### Option 2: Right-click Method
1. Right-click the `ankra-cli` binary in Finder
2. Select **"Open"** from the context menu
3. Click **"Open"** in the security dialog

## Building from Source (No Security Warnings)

If you prefer to build from source to avoid security warnings entirely:

```bash
git clone https://github.com/your-org/ankra-cli.git
cd ankra-cli
go build -o ankra
sudo mv ankra /usr/local/bin/
```

## Why This Happens

- **Local builds**: When you build with `go build`, macOS doesn't apply quarantine attributes
- **Downloaded binaries**: Binaries downloaded from the internet get quarantine attributes
- **Code signing**: Apple Developer Program membership ($99/year) is required for proper code signing
- **Workaround**: The `xattr` command removes the quarantine attribute, making the binary trusted

## Future Plans

We plan to implement proper code signing and notarization once we have Apple Developer Program membership. Until then, the methods above are safe to use.
