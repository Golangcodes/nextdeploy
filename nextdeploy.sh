#!/bin/sh
set -e

# NextDeploy CLI Installer for Linux/macOS

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "Installing NextDeploy CLI for $OS/$ARCH..."

# Define download URL
DOWNLOAD_URL="https://github.com/Golangcodes/nextdeploy/releases/latest/download/nextdeploy-$OS-$ARCH"

if [ "$OS" = "darwin" ]; then
    echo "macOS detected. Downloading..."
    curl -fLo nextdeploy "$DOWNLOAD_URL"
    chmod +x nextdeploy
    sudo mv nextdeploy /usr/local/bin/
else
    echo "Linux detected. Downloading..."
    curl -fLo nextdeploy "$DOWNLOAD_URL"
    chmod +x nextdeploy
    sudo mv nextdeploy /usr/local/bin/
fi

echo "✅ NextDeploy CLI installed successfully!"
echo "Run 'nextdeploy' to get started."
