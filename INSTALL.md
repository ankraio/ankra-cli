# Ankra CLI Installation Guide

This guide provides step-by-step instructions for installing the Ankra CLI on your system.

---

## 🚀 Installation Steps

1. **Download and Run the Installer**

   For most users (requires sudo for global install):

   ```sh
   curl -sSL https://artifact.infra.ankra.cloud/repository/ankra-install-public/cli/install.sh | bash
   ```

   For a user-local install (no sudo, adds to ~/bin):

   ```sh
   curl -sSL https://artifact.infra.ankra.cloud/repository/ankra-install-public/cli/install-simple.sh | bash
   ```

---

## 🛠️ Next Steps

1. **Configure Authentication:**
   ```sh
   export ANKRA_API_TOKEN=your-api-token
   ```
2. **Set Platform URL (optional):**
   ```sh
   export ANKRA_BASE_URL=https://platform.ankra.app
   ```
3. **Add to shell profile for persistence:**
   - For Zsh:
     ```sh
     echo 'export ANKRA_API_TOKEN=your-token' >> ~/.zshrc
     echo 'export ANKRA_BASE_URL=https://platform.ankra.app' >> ~/.zshrc
     ```
   - For Bash:
     ```sh
     echo 'export ANKRA_API_TOKEN=your-token' >> ~/.bashrc
     echo 'export ANKRA_BASE_URL=https://platform.ankra.app' >> ~/.bashrc
     ```
4. **Reload your shell profile:**
   - For Zsh:
     ```sh
     source ~/.zshrc
     ```
   - For Bash:
     ```sh
     source ~/.bashrc
     ```
5. **Select a cluster:**
   ```sh
   ankra select cluster
   ```
6. **Deploy addons:**
   ```sh
   ankra apply fluent-bit
   ankra apply cert-manager
   ```
7. **Get started:**
   ```sh
   ankra --help
   ```

---

## ℹ️ Additional Information

- **Documentation:** [https://docs.ankra.io](https://docs.ankra.io)
- **Support:** hello@ankra.io

---

**Note:** If you encounter issues, ensure that your `$PATH` includes the install directory (usually `/usr/local/bin` or `~/bin`). You may need to restart your terminal after installation.
