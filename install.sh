#!/usr/bin/env bash

set -euo pipefail

# =============================================================================
# Configuration
# =============================================================================

REPO_URL="https://raw.githubusercontent.com/ichinya/mtproxy-installer/main"
INSTALL_DIR="${INSTALL_DIR:-/opt/mtproxy-installer}"
PROVIDER="${1:-${PROVIDER:-telemt}}"
# PORT can be passed as second positional arg or via environment
PORT="${2:-${PORT:-443}}"
PROVIDER_DIR="${INSTALL_DIR}/providers/${PROVIDER}"
DATA_DIR="${PROVIDER_DIR}/data"
DEFAULT_TELEMT_IMAGE_SOURCE="whn0thacked/telemt-docker:latest"
DEFAULT_MTG_IMAGE_SOURCE="ghcr.io/9seconds/mtg:latest"

# Defaults (can be overridden via environment)
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

set_env_value() {
    local file="$1"
    local key="$2"
    local value="$3"
    local tmp

    tmp="$(mktemp)"

    if [ -f "${file}" ]; then
        awk -v key="${key}" -v value="${value}" '
            BEGIN { updated = 0 }
            index($0, key "=") == 1 {
                print key "=" value
                updated = 1
                next
            }
            { print }
            END {
                if (!updated) {
                    print key "=" value
                }
            }
        ' "${file}" > "${tmp}"
    else
        printf '%s=%s\n' "${key}" "${value}" > "${tmp}"
    fi

    mv "${tmp}" "${file}"
}

resolve_image_ref() {
    local source_ref="$1"
    local pinned_ref

    docker pull "${source_ref}" >/dev/null
    pinned_ref="$(docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "${source_ref}" 2>/dev/null | sed '/^$/d' | head -n1 || true)"

    if [ -n "${pinned_ref}" ]; then
        printf '%s\n' "${pinned_ref}"
    else
        printf '%s\n' "${source_ref}"
    fi
}

generate_secret() {
    case "${PROVIDER}" in
        telemt)
            openssl rand -hex 16
        ;;
        mtg)
            local image="${MTG_IMAGE_SOURCE:-${MTG_IMAGE:-${DEFAULT_MTG_IMAGE_SOURCE}}}"
            docker run --rm "${image}" generate-secret "${TLS_DOMAIN}"
        ;;
        *)
            die "Unknown provider: ${PROVIDER}"
        ;;
    esac
}

# =============================================================================
# Provider: telemt
# =============================================================================

write_telemt_env() {
    # Root .env for docker compose
    cat > "${INSTALL_DIR}/.env" <<EOF
PROVIDER=telemt
PORT=${PORT}
API_PORT=${API_PORT}
PUBLIC_IP=${PUBLIC_IP}
TELEMT_IMAGE_SOURCE=${TELEMT_IMAGE_SOURCE:-${DEFAULT_TELEMT_IMAGE_SOURCE}}
TELEMT_IMAGE=${TELEMT_IMAGE:-${TELEMT_IMAGE_SOURCE:-${DEFAULT_TELEMT_IMAGE_SOURCE}}}
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
    # Root .env for docker compose
    cat > "${INSTALL_DIR}/.env" <<EOF
PROVIDER=mtg
PORT=${PORT}
PUBLIC_IP=${PUBLIC_IP}
MTG_IMAGE_SOURCE=${MTG_IMAGE_SOURCE:-${DEFAULT_MTG_IMAGE_SOURCE}}
MTG_IMAGE=${MTG_IMAGE:-${MTG_IMAGE_SOURCE:-${DEFAULT_MTG_IMAGE_SOURCE}}}
MTG_DEBUG=${MTG_DEBUG:-info}
TLS_DOMAIN=${TLS_DOMAIN}
SECRET=${SECRET}
EOF
}

write_mtg_config() {
    local debug_flag=false
    if [ "${MTG_DEBUG:-info}" = "debug" ] || [ "${MTG_DEBUG:-info}" = "true" ] || [ "${MTG_DEBUG:-info}" = "1" ]; then
        debug_flag=true
    fi
    
    cat > "${PROVIDER_DIR}/mtg.conf" <<EOF
secret = "${SECRET}"
bind-to = "0.0.0.0:3128"
debug = ${debug_flag}
EOF
}

write_mtg_compose() {
    cat > "${INSTALL_DIR}/docker-compose.yml" <<'EOF'
services:
  mtg:
    image: ${MTG_IMAGE:-ghcr.io/9seconds/mtg:latest}
    container_name: mtg
    restart: unless-stopped
    volumes:
      - ./providers/mtg/mtg.conf:/config.toml:ro
      - ./providers/mtg/data:/var/lib/mtg
    ports:
      - "${PORT}:3128/tcp"
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

validate_mtg_config() {
    log_fix "Validating generated mtg config with mtg access."
    docker run --rm \
        -v "${PROVIDER_DIR}/mtg.conf:/config.toml:ro" \
        "${MTG_IMAGE}" \
        access /config.toml >/dev/null
}

prepare_provider_image() {
    local env_file="${INSTALL_DIR}/.env"
    local source_ref
    local pinned_ref

    case "${PROVIDER}" in
        telemt)
            source_ref="${TELEMT_IMAGE_SOURCE:-${TELEMT_IMAGE:-${DEFAULT_TELEMT_IMAGE_SOURCE}}}"
            log "Pulling Telemt image source: ${source_ref}"
            pinned_ref="$(resolve_image_ref "${source_ref}")"
            export TELEMT_IMAGE_SOURCE="${source_ref}"
            export TELEMT_IMAGE="${pinned_ref}"
            set_env_value "${env_file}" "TELEMT_IMAGE_SOURCE" "${TELEMT_IMAGE_SOURCE}"
            set_env_value "${env_file}" "TELEMT_IMAGE" "${TELEMT_IMAGE}"
        ;;
        mtg)
            source_ref="${MTG_IMAGE_SOURCE:-${MTG_IMAGE:-${DEFAULT_MTG_IMAGE_SOURCE}}}"
            log "Pulling mtg image source: ${source_ref}"
            pinned_ref="$(resolve_image_ref "${source_ref}")"
            export MTG_IMAGE_SOURCE="${source_ref}"
            export MTG_IMAGE="${pinned_ref}"
            set_env_value "${env_file}" "MTG_IMAGE_SOURCE" "${MTG_IMAGE_SOURCE}"
            set_env_value "${env_file}" "MTG_IMAGE" "${MTG_IMAGE}"
        ;;
    esac
}

urlencode() {
    local str="$1"
    # Portable fallback if python3 not present
    if command -v python3 >/dev/null 2>&1; then
        python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=''))" "$str"
        elif command -v python >/dev/null 2>&1; then
        python -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=''))" "$str"
    else
        # Not ideal, but dot and alnum are safe; fallback means secret may break on some clients
        printf '%s' "$str"
    fi
}

get_mtg_link() {
    local encoded_secret="$(urlencode "${SECRET}")"
    printf 'tg://proxy?server=%s&port=%s&secret=%s\n' "${PUBLIC_IP}" "${PORT}" "${encoded_secret}"
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

validate_provider_files() {
    case "${PROVIDER}" in
        telemt) return 0 ;;
        mtg)    validate_mtg_config ;;
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
    
    # Set provider-specific defaults
    case "${PROVIDER}" in
        telemt)
            export TELEMT_IMAGE_SOURCE="${TELEMT_IMAGE_SOURCE:-${TELEMT_IMAGE:-${DEFAULT_TELEMT_IMAGE_SOURCE}}}"
            export RUST_LOG="${RUST_LOG:-info}"
        ;;
        mtg)
            export MTG_IMAGE_SOURCE="${MTG_IMAGE_SOURCE:-${MTG_IMAGE:-${DEFAULT_MTG_IMAGE_SOURCE}}}"
            export MTG_DEBUG="${MTG_DEBUG:-info}"
        ;;
    esac
    
    PUBLIC_IP="${PUBLIC_IP:-$(curl -fsSL https://api.ipify.org)}"
    SECRET="${SECRET:-$(generate_secret)}"
    export PORT PUBLIC_IP SECRET TLS_DOMAIN
    
    setup_provider
    write_provider_files
    prepare_provider_image
    validate_provider_files
    
    log "Starting ${PROVIDER}..."
    docker compose -f "${INSTALL_DIR}/docker-compose.yml" --project-directory "${INSTALL_DIR}" --env-file "${INSTALL_DIR}/.env" down || true
    docker compose -f "${INSTALL_DIR}/docker-compose.yml" --project-directory "${INSTALL_DIR}" --env-file "${INSTALL_DIR}/.env" up -d --force-recreate
    
    sleep 3
    PROXY_LINK="$(get_proxy_link)" || true
    
    print_info
}

main "$@"
