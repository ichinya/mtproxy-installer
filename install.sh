#!/usr/bin/env bash

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/mtproxy-installer}"
PROVIDER="telemt"
PROVIDER_DIR="${INSTALL_DIR}/providers/${PROVIDER}"
DATA_DIR="${PROVIDER_DIR}/data"

PORT="${PORT:-443}"
API_PORT="${API_PORT:-9091}"
TELEMT_IMAGE="${TELEMT_IMAGE:-whn0thacked/telemt-docker:latest}"
RUST_LOG="${RUST_LOG:-info}"
TLS_DOMAIN="${TLS_DOMAIN:-www.google.com}"
PROXY_USER="${PROXY_USER:-main}"

log() {
    printf '%s\n' "$1"
}

die() {
    printf 'Error: %s\n' "$1" >&2
    exit 1
}

require_root() {
    if [ "${EUID}" -ne 0 ]; then
        die "Run as root. Example: curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo bash"
    fi
}

ensure_apt_package() {
    local package="$1"

    if dpkg -s "${package}" >/dev/null 2>&1; then
        return 0
    fi

    apt-get update
    apt-get install -y "${package}"
}

ensure_base_tools() {
    if ! command -v curl >/dev/null 2>&1; then
        ensure_apt_package curl
    fi

    if ! command -v openssl >/dev/null 2>&1; then
        ensure_apt_package openssl
    fi

    if ! command -v docker >/dev/null 2>&1; then
        log "Docker not found. Installing..."
        curl -fsSL https://get.docker.com | sh
    fi

    if ! docker compose version >/dev/null 2>&1; then
        log "Docker Compose plugin not found. Installing..."
        ensure_apt_package docker-compose-plugin
    fi
}

backup_if_exists() {
    local path="$1"

    if [ -f "${path}" ]; then
        cp "${path}" "${path}.bak.$(date +%s)"
    fi
}

write_root_compose() {
    cat > "${INSTALL_DIR}/docker-compose.yml" <<'EOF'
services:
  telemt:
    image: ${TELEMT_IMAGE:-whn0thacked/telemt-docker:latest}
    container_name: telemt
    restart: unless-stopped
    environment:
      RUST_LOG: ${RUST_LOG:-info}
    volumes:
      - ./providers/telemt/telemt.toml:/etc/telemt.toml:ro
      - ./providers/telemt/data:/var/lib/telemt
    ports:
      - "${PORT}:443/tcp"
      - "127.0.0.1:${API_PORT}:9091/tcp"
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
    read_only: true
    tmpfs:
      - /tmp:rw,nosuid,nodev,noexec,size=16m
    ulimits:
      nofile:
        soft: 65536
        hard: 65536
EOF
}

write_provider_compose() {
    cat > "${PROVIDER_DIR}/docker-compose.yml" <<'EOF'
services:
  telemt:
    image: ${TELEMT_IMAGE:-whn0thacked/telemt-docker:latest}
    container_name: telemt
    restart: unless-stopped
    environment:
      RUST_LOG: ${RUST_LOG:-info}
    volumes:
      - ./telemt.toml:/etc/telemt.toml:ro
      - ./data:/var/lib/telemt
    ports:
      - "${PORT}:443/tcp"
      - "127.0.0.1:${API_PORT}:9091/tcp"
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
    read_only: true
    tmpfs:
      - /tmp:rw,nosuid,nodev,noexec,size=16m
    ulimits:
      nofile:
        soft: 65536
        hard: 65536
EOF
}

write_env_file() {
    cat > "${INSTALL_DIR}/.env" <<EOF
PORT=${PORT}
API_PORT=${API_PORT}
TELEMT_IMAGE=${TELEMT_IMAGE}
RUST_LOG=${RUST_LOG}
EOF

    cat > "${PROVIDER_DIR}/.env" <<EOF
PORT=${PORT}
API_PORT=${API_PORT}
TELEMT_IMAGE=${TELEMT_IMAGE}
RUST_LOG=${RUST_LOG}
EOF
}

write_telemt_config() {
    local public_ip="$1"
    local secret="$2"

    cat > "${PROVIDER_DIR}/telemt.toml" <<EOF
[general]
use_middle_proxy = true
proxy_secret_path = "/var/lib/telemt/proxy-secret"
middle_proxy_nat_ip = "${public_ip}"
middle_proxy_nat_probe = false
log_level = "normal"

[general.modes]
classic = false
secure = false
tls = true

[general.links]
show = "*"
public_host = "${public_ip}"
public_port = ${PORT}

[server]
port = 443
listen_addr_ipv4 = "0.0.0.0"
listen_addr_ipv6 = "::"
proxy_protocol = false
metrics_whitelist = ["127.0.0.1/32", "::1/128"]

[server.api]
enabled = true
listen = "0.0.0.0:9091"
whitelist = []
read_only = true

[[server.listeners]]
ip = "0.0.0.0"
announce = "${public_ip}"

[censorship]
tls_domain = "${TLS_DOMAIN}"
mask = true
mask_port = 443
fake_cert_len = 2048
tls_emulation = true
tls_front_dir = "/var/lib/telemt/tlsfront"

[access.users]
"${PROXY_USER}" = "${secret}"
EOF
}

wait_for_api() {
    local url="http://127.0.0.1:${API_PORT}/v1/health"
    local attempt

    for attempt in $(seq 1 60); do
        if curl -fsS "${url}" >/dev/null 2>&1; then
            return 0
        fi

        sleep 2
    done

    return 1
}

extract_proxy_link() {
    local users_json="$1"

    printf '%s' "${users_json}" |
        tr -d '\n' |
        sed -n 's/.*"tls":\["\([^"]*\)"\].*/\1/p' |
        head -n 1
}

main() {
    local secret
    local public_ip
    local users_json
    local proxy_link=""

    require_root
    ensure_base_tools

    log "Preparing install directory: ${INSTALL_DIR}"
    mkdir -p "${PROVIDER_DIR}" "${DATA_DIR}/cache" "${DATA_DIR}/tlsfront"
    chown -R 65532:65532 "${DATA_DIR}"

    backup_if_exists "${INSTALL_DIR}/docker-compose.yml"
    backup_if_exists "${INSTALL_DIR}/.env"
    backup_if_exists "${PROVIDER_DIR}/docker-compose.yml"
    backup_if_exists "${PROVIDER_DIR}/.env"
    backup_if_exists "${PROVIDER_DIR}/telemt.toml"

    secret="$(openssl rand -hex 16)"
    public_ip="$(curl -fsSL https://api.ipify.org)"

    write_root_compose
    write_provider_compose
    write_env_file
    write_telemt_config "${public_ip}" "${secret}"

    log "Starting Telemt..."
    docker compose -f "${INSTALL_DIR}/docker-compose.yml" --project-directory "${INSTALL_DIR}" --env-file "${INSTALL_DIR}/.env" up -d

    if wait_for_api; then
        users_json="$(curl -fsS "http://127.0.0.1:${API_PORT}/v1/users" || true)"
        if [ -n "${users_json}" ]; then
            proxy_link="$(extract_proxy_link "${users_json}")"
        fi
    fi

    log ""
    log "=============================="
    log "Telemt installed"
    log "=============================="
    log ""
    log "Install dir: ${INSTALL_DIR}"
    log "Provider: ${PROVIDER}"
    log "Public endpoint: ${public_ip}:${PORT}"
    log "Image: ${TELEMT_IMAGE}"
    log "TLS domain: ${TLS_DOMAIN}"
    log "User: ${PROXY_USER}"
    log "Secret: ${secret}"
    log ""

    if [ -n "${proxy_link}" ]; then
        log "Proxy link:"
        log "${proxy_link}"
        log ""
    else
        log "Proxy link was not extracted automatically yet."
        log "Try after a minute: curl -fsS http://127.0.0.1:${API_PORT}/v1/users"
        log ""
    fi

    log "Local Telemt API: http://127.0.0.1:${API_PORT}/v1/health"
    log "Config: ${PROVIDER_DIR}/telemt.toml"
    log "Logs: docker compose -f ${INSTALL_DIR}/docker-compose.yml --project-directory ${INSTALL_DIR} --env-file ${INSTALL_DIR}/.env logs -f telemt"
}

main "$@"
