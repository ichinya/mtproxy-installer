#!/usr/bin/env bash

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/mtproxy-installer}"
KEEP_DATA="${KEEP_DATA:-false}"

log() {
    printf '%s\n' "$1"
}

die() {
    printf 'Error: %s\n' "$1" >&2
    exit 1
}

require_root() {
    if [ "${EUID}" -ne 0 ]; then
        die "Run as root. Example: curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/uninstall.sh | sudo bash"
    fi
}

check_installation() {
    if [ ! -d "${INSTALL_DIR}" ]; then
        die "Installation directory not found: ${INSTALL_DIR}"
    fi
}

stop_container() {
    local compose_file="${INSTALL_DIR}/docker-compose.yml"
    local env_file="${INSTALL_DIR}/.env"

    if [ -f "${compose_file}" ]; then
        log "Stopping Telemt container..."
        docker compose -f "${compose_file}" --project-directory "${INSTALL_DIR}" --env-file "${env_file}" down --remove-orphans 2>/dev/null || true
    fi
}

remove_image() {
    local image="whn0thacked/telemt-docker:latest"

    log "Removing Telemt image..."
    docker rmi "${image}" 2>/dev/null || log "Image not found or already removed"
}

remove_data() {
    if [ "${KEEP_DATA}" = "true" ]; then
        log "Keeping data directory (KEEP_DATA=true)"
        return
    fi

    log "Removing installation directory..."
    rm -rf "${INSTALL_DIR}"
}

main() {
    require_root
    check_installation

    log "=============================="
    log "Telemt Uninstaller"
    log "=============================="
    log ""
    log "Install dir: ${INSTALL_DIR}"
    log "Keep data: ${KEEP_DATA}"
    log ""

    stop_container
    remove_image
    remove_data

    log ""
    log "=============================="
    log "Uninstall complete"
    log "=============================="
    log ""
    log "To reinstall, run:"
    log "curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo bash"
}

main "$@"
