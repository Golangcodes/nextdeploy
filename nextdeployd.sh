#!/bin/sh
set -e

# NextDeploy Daemon Installer for Ubuntu/Linux Servers

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

if [ "$OS" != "linux" ]; then
    echo "NextDeploy Daemon only supports Linux servers."
    exit 1
fi

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

echo "Installing NextDeploy Daemon for Linux/$ARCH..."

# Check if running as root
if [ "$(id -u)" != "0" ]; then
   echo "This script must be run as root to install the daemon service."
   echo "Please run: curl https://nextdeploy.one/nextdeployd.sh | sudo sh"
   exit 1
fi

# 1. Check for docker
if ! command -v docker >/dev/null 2>&1; then
    echo "Docker is not installed. Installing Docker..."
    curl -fsSL https://get.docker.com -o get-docker.sh
    sh get-docker.sh
    rm get-docker.sh
fi

# Define download URL
DOWNLOAD_URL="https://github.com/Golangcodes/nextdeploy/releases/latest/download/nextdeployd-$OS-$ARCH"

# 2. Download and install binary
echo "Downloading..."
curl -fLo nextdeployd "$DOWNLOAD_URL"
chmod +x nextdeployd
mv nextdeployd /usr/local/bin/

# 3. Create necessary directories
mkdir -p /etc/nextdeployd
mkdir -p /var/log/nextdeployd

# 4. Setup systemd service
cat > /etc/systemd/system/nextdeployd.service << 'EOF'
[Unit]
Description=NextDeploy Daemon
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/nextdeployd
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

# 5. Enable and start service
systemctl daemon-reload
systemctl enable nextdeployd
systemctl start nextdeployd

echo "NextDeploy Daemon installed and started successfully!"
echo "Check status: systemctl status nextdeployd"
