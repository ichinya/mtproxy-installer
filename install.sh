#!/usr/bin/env bash

set -e

echo "Installing MTProxy..."

if ! command -v docker &> /dev/null
then
    echo "Docker not found. Installing..."
    curl -fsSL https://get.docker.com | sh
fi

if ! command -v docker compose &> /dev/null
then
    echo "Installing docker compose plugin..."
    apt-get update
    apt-get install -y docker-compose-plugin
fi

echo "Generating secret..."

SECRET=$(openssl rand -hex 16)

cat <<EOF > .env
SECRET=$SECRET
PORT=443
EOF

echo "Starting MTProxy..."

docker compose up -d

IP=$(curl -s https://api.ipify.org)

echo ""
echo "=============================="
echo "MTProxy installed"
echo "=============================="
echo ""
echo "Proxy link:"
echo "tg://proxy?server=$IP&port=443&secret=$SECRET"
echo ""
echo "For MTProxy bot registration send:"
echo ""
echo "$IP:443"
echo ""
