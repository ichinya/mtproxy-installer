#!/usr/bin/env bash

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/mtproxy-installer}"
PROVIDER="telemt"
PROVIDER_DIR="${INSTALL_DIR}/providers/${PROVIDER}"

log() {
    printf '%s\n' "$1"
}

die() {
    printf 'Error: %s\n' "$1" >&2
    exit 1
}

require_root() {
    if [ "${EUID}" -ne 0 ]; then
        die "Run as root. Example: curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/update.sh | sudo bash"
    fi
}

check_installation() {
    if [ ! -d "${INSTALL_DIR}" ]; then
        die "Installation directory not found: ${INSTALL_DIR}"
    fi

    if [ ! -f "${PROVIDER_DIR}/telemt.toml" ]; then
        die "Telemt config not found: ${PROVIDER_DIR}/telemt.toml"
    fi
}

pull_image() {
    local image="whn0thacked/telemt-docker:latest"

    log "Pulling latest Telemt image..."
    docker pull "${image}"
}

restart_container() {
    local compose_file="${INSTALL_DIR}/docker-compose.yml"
    local env_file="${INSTALL_DIR}/.env"

    log "Restarting Telemt..."
    docker compose -f "${compose_file}" --project-directory "${INSTALL_DIR}" --env-file "${env_file}" up -d --pull always
}

wait_for_api() {
    local api_port
    api_port="$(grep -E '^API_PORT=' "${env_file}" 2>/dev/null | cut -d= -f2 || echo "9091")"
    local url="http://127.0.0.1:${api_port}/v1/health"
    local attempt

    log "Waiting for API..."

    for attempt in $(seq 1 30); do
        if curl -fsS "${url}" >/dev/null 2>&1; then
            log "API is healthy"
            return 0
        fi

        sleep 2
    done

    log "Warning: API health check timed out"
    return 0
}

show_version() {
    local api_port
    api_port="$(grep -E '^API_PORT=' "${INSTALL_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo "9091")"

    log ""
    log "Check logs: docker compose -f ${INSTALL_DIR}/docker-compose.yml --project-directory ${INSTALL_DIR} --env-file ${INSTALL_DIR}/.env logs -f telemt"
    log "Health check: curl http://127.0.0.1:${api_port}/v1/health"
}

main() {
    require_root
    check_installation

    log "=============================="
    log "Telemt Updater"
    log "=============================="
    log ""
    log "Install dir: ${INSTALL_DIR}"
    log ""

    pull_image
    restart_container
    wait_for_api
    show_version

    log ""
    log "=============================="
    log "Update complete"
    log "=============================="
}

main "$@"
