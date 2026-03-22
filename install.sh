#!/usr/bin/env bash

set -euo pipefail

# =============================================================================
# Configuration
# =============================================================================

REPO_URL="https://raw.githubusercontent.com/ichinya/mtproxy-installer/main"
INSTALL_DIR="${INSTALL_DIR:-/opt/mtproxy-installer}"
PROVIDER="${PROVIDER:-telemt}"
PROVIDER_DIR="${INSTALL_DIR}/providers/${PROVIDER}"
DATA_DIR="${PROVIDER_DIR}/data"

# Defaults (can be overridden via environment)
PORT="${PORT:-443}"
API_PORT="${API_PORT:-9091}"
TLS_DOMAIN="${TLS_DOMAIN:-www.wikipedia.org}"

# =============================================================================
# Logging
# =============================================================================

log() { printf '%s\n' "$1"; }
die() { printf 'Error: %s\n' "$1" >&2; exit 1; }
log_fix() { log "[FIX] $1"; }

# =============================================================================
# Prerequisites
# =============================================================================

require_root() {
    if [ "${EUID}" -ne 0 ]; then
        die "Run as root. Example: curl -fsSL .../install.sh | sudo bash"
    fi
}

ensure_apt_package() {
    local package="$1"
    dpkg -s "${package}" >/dev/null 2>&1 || { apt-get update && apt-get install -y "${package}"; }
}

ensure_base_tools() {
    command -v curl >/dev/null 2>&1 || ensure_apt_package curl
    command -v openssl >/dev/null 2>&1 || ensure_apt_package openssl
    command -v docker >/dev/null 2>&1 || { log "Installing Docker..."; curl -fsSL https://get.docker.com | sh; }
    docker compose version >/dev/null 2>&1 || ensure_apt_package docker-compose-plugin
}

# =============================================================================
# Utils
# =============================================================================

backup_if_exists() {
    if [ -f "$1" ]; then
        cp "$1" "$1.bak.$(date +%s)"
    fi
}

generate_secret() {
    case "${PROVIDER}" in
        telemt) openssl rand -hex 16 ;;
        mtg)    printf 'dd%s\n' "$(openssl rand -hex 16)" ;;
        *)      die "Unknown provider: ${PROVIDER}" ;;
    esac
}

# =============================================================================
# Provider: telemt
# =============================================================================

write_telemt_env() {
    cat > "${PROVIDER_DIR}/.env" <<EOF
PORT=${PORT}
API_PORT=${API_PORT}
PUBLIC_IP=${PUBLIC_IP}
TELEMT_IMAGE=${TELEMT_IMAGE:-whn0thacked/telemt-docker:latest}
RUST_LOG=${RUST_LOG:-info}
TLS_DOMAIN=${TLS_DOMAIN}
PROXY_USER=${PROXY_USER:-main}
SECRET=${SECRET}
EOF
}

write_telemt_config() {
    cat > "${PROVIDER_DIR}/telemt.toml" <<EOF
[general]
use_middle_proxy = true
proxy_secret_path = "/var/lib/telemt/proxy-secret"
middle_proxy_nat_ip = "${PUBLIC_IP}"
middle_proxy_nat_probe = true
log_level = "normal"

[general.modes]
classic = false
secure = false
tls = true

[general.links]
show = "*"
public_host = "${PUBLIC_IP}"
public_port = ${PORT}

[server]
port = 443
listen_addr_ipv4 = "0.0.0.0"
listen_addr_ipv6 = "::"
proxy_protocol = false
metrics_whitelist = ["127.0.0.1/32", "::1/128"]

[server.api]
enabled = true
listen = "0.0.0.0:${API_PORT}"
whitelist = []
read_only = true

[[server.listeners]]
ip = "0.0.0.0"
announce = "${PUBLIC_IP}"

[censorship]
tls_domain = "${TLS_DOMAIN}"
mask = true
mask_port = 443
fake_cert_len = 2048
tls_emulation = false
tls_front_dir = "/var/lib/telemt/tlsfront"

[access.users]
"${PROXY_USER:-main}" = "${SECRET}"
EOF
}

write_telemt_compose() {
    cat > "${INSTALL_DIR}/docker-compose.yml" <<'EOF'
services:
  telemt:
    image: ${TELEMT_IMAGE}
    container_name: telemt
    restart: unless-stopped
    environment:
      RUST_LOG: ${RUST_LOG}
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

get_telemt_link() {
    local url="http://127.0.0.1:${API_PORT}/v1/health"
    local attempt

    for attempt in $(seq 1 30); do
        if curl -fsS "${url}" >/dev/null 2>&1; then
            curl -fsS "http://127.0.0.1:${API_PORT}/v1/users" 2>/dev/null | \
                tr -d '\n' | sed -n 's/.*"tls":\["\([^"]*\)"\].*/\1/p' | head -n1
            return 0
        fi
        sleep 2
    done
    return 1
}

# =============================================================================
# Provider: mtg
# =============================================================================

write_mtg_env() {
    cat > "${PROVIDER_DIR}/.env" <<EOF
PORT=${PORT}
PUBLIC_IP=${PUBLIC_IP}
MTG_IMAGE=${MTG_IMAGE:-ghcr.io/9seconds/mtg:latest}
MTG_DEBUG=${MTG_DEBUG:-info}
TLS_DOMAIN=${TLS_DOMAIN}
SECRET=${SECRET}
EOF
}

write_mtg_config() {
    cat > "${PROVIDER_DIR}/mtg.conf" <<EOF
bind = "0.0.0.0:443"
advertise = "${PUBLIC_IP}:${PORT}"
secret = "${SECRET}"
tls-domain = "${TLS_DOMAIN}"
debug = "${MTG_DEBUG:-info}"
EOF
}

write_mtg_compose() {
    cat > "${INSTALL_DIR}/docker-compose.yml" <<'EOF'
services:
  mtg:
    image: ${MTG_IMAGE}
    container_name: mtg
    restart: unless-stopped
    environment:
      MTG_DEBUG: ${MTG_DEBUG}
    volumes:
      - ./providers/mtg/mtg.conf:/etc/mtg.conf:ro
      - ./providers/mtg/data:/var/lib/mtg
    ports:
      - "${PORT}:443/tcp"
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

get_mtg_link() {
    printf 'tg://proxy?server=%s&port=%s&secret=%s\n' "${PUBLIC_IP}" "${PORT}" "${SECRET}"
}

# =============================================================================
# Provider Dispatcher
# =============================================================================

validate_provider() {
    case "${PROVIDER}" in
        telemt|mtg) return 0 ;;
        *) die "Unsupported provider: ${PROVIDER}. Supported: telemt, mtg" ;;
    esac
}

setup_provider() {
    mkdir -p "${PROVIDER_DIR}" "${DATA_DIR}"
    [ "${PROVIDER}" = "telemt" ] && mkdir -p "${DATA_DIR}/cache" "${DATA_DIR}/tlsfront"
    chown -R 65532:65532 "${DATA_DIR}"

    backup_if_exists "${INSTALL_DIR}/docker-compose.yml"
    backup_if_exists "${PROVIDER_DIR}/.env"
    backup_if_exists "${PROVIDER_DIR}/telemt.toml"
    backup_if_exists "${PROVIDER_DIR}/mtg.conf"
}

write_provider_files() {
    case "${PROVIDER}" in
        telemt)
            write_telemt_compose
            write_telemt_env
            write_telemt_config
            ;;
        mtg)
            write_mtg_compose
            write_mtg_env
            write_mtg_config
            ;;
    esac
}

get_proxy_link() {
    case "${PROVIDER}" in
        telemt) get_telemt_link ;;
        mtg)    get_mtg_link ;;
    esac
}

print_info() {
    log ""
    log "=============================="
    log "${PROVIDER} installed"
    log "=============================="
    log ""
    log "Install dir: ${INSTALL_DIR}"
    log "Provider: ${PROVIDER}"
    log "Public endpoint: ${PUBLIC_IP}:${PORT}"
    log "Secret: ${SECRET}"
    log ""

    [ -n "${PROXY_LINK:-}" ] && { log "Proxy link:"; log "${PROXY_LINK}"; log ""; }

    case "${PROVIDER}" in
        telemt)
            log "API: http://127.0.0.1:${API_PORT}/v1/health"
            log "Config: ${PROVIDER_DIR}/telemt.toml"
            log ""
            log_fix "Telegram voice calls are not guaranteed over MTProto proxy."
            ;;
        mtg)
            log "Config: ${PROVIDER_DIR}/mtg.conf"
            log ""
            log_fix "mtg v2 does not support ad_tag."
            log_fix "mtg has no HTTP API for automatic link extraction."
            log_fix "Telegram voice calls are not guaranteed over MTProto proxy."
            ;;
    esac

    log ""
    log "Logs: docker compose -f ${INSTALL_DIR}/docker-compose.yml logs -f ${PROVIDER}"
}

# =============================================================================
# Main
# =============================================================================

main() {
    require_root
    validate_provider
    ensure_base_tools

    log "================================"
    log "MTProxy Installer"
    log "================================"
    log "Provider: ${PROVIDER}"

    PUBLIC_IP="${PUBLIC_IP:-$(curl -fsSL https://api.ipify.org)}"
    SECRET="${SECRET:-$(generate_secret)}"

    setup_provider
    write_provider_files

    log "Starting ${PROVIDER}..."
    docker compose -f "${INSTALL_DIR}/docker-compose.yml" --project-directory "${INSTALL_DIR}" up -d

    sleep 3
    PROXY_LINK="$(get_proxy_link)" || true

    print_info
}

main "$@"
