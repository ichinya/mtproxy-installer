#!/usr/bin/env bash

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/mtproxy-installer}"
ENV_FILE="${INSTALL_DIR}/.env"
COMPOSE_FILE="${INSTALL_DIR}/docker-compose.yml"
DEFAULT_TELEMT_IMAGE_SOURCE="whn0thacked/telemt-docker:latest"
DEFAULT_MTG_IMAGE_SOURCE="nineseconds/mtg:2"

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

get_env_value() {
    local file="$1"
    local key="$2"
    local default_value="${3:-}"
    local line

    if [ ! -f "${file}" ]; then
        printf '%s\n' "${default_value}"
        return 0
    fi

    line="$(grep -E "^${key}=" "${file}" 2>/dev/null | tail -n1 || true)"
    if [ -n "${line}" ]; then
        printf '%s\n' "${line#*=}"
    else
        printf '%s\n' "${default_value}"
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

provider_image_var() {
    case "$1" in
        telemt) printf 'TELEMT_IMAGE\n' ;;
        mtg)    printf 'MTG_IMAGE\n' ;;
        *)      die "Unknown provider: $1" ;;
    esac
}

provider_source_var() {
    case "$1" in
        telemt) printf 'TELEMT_IMAGE_SOURCE\n' ;;
        mtg)    printf 'MTG_IMAGE_SOURCE\n' ;;
        *)      die "Unknown provider: $1" ;;
    esac
}

provider_default_source() {
    case "$1" in
        telemt) printf '%s\n' "${DEFAULT_TELEMT_IMAGE_SOURCE}" ;;
        mtg)    printf '%s\n' "${DEFAULT_MTG_IMAGE_SOURCE}" ;;
        *)      die "Unknown provider: $1" ;;
    esac
}

provider_config_path() {
    case "$1" in
        telemt) printf '%s\n' "${INSTALL_DIR}/providers/telemt/telemt.toml" ;;
        mtg)    printf '%s\n' "${INSTALL_DIR}/providers/mtg/mtg.conf" ;;
        *)      die "Unknown provider: $1" ;;
    esac
}

resolve_image_ref() {
    local source_ref="$1"
    local pinned_ref

    printf 'Pulling image source: %s\n' "${source_ref}" >&2
    docker pull "${source_ref}" >/dev/null
    pinned_ref="$(docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "${source_ref}" 2>/dev/null | sed '/^$/d' | head -n1 || true)"

    if [ -n "${pinned_ref}" ]; then
        printf '%s\n' "${pinned_ref}"
    else
        printf '%s\n' "${source_ref}"
    fi
}

detect_provider() {
    local configured

    configured="$(get_env_value "${ENV_FILE}" "PROVIDER" "")"
    case "${configured}" in
        telemt|mtg)
            printf '%s\n' "${configured}"
            return 0
        ;;
    esac

    if [ -f "${INSTALL_DIR}/providers/telemt/telemt.toml" ]; then
        printf 'telemt\n'
        return 0
    fi

    if [ -f "${INSTALL_DIR}/providers/mtg/mtg.conf" ]; then
        printf 'mtg\n'
        return 0
    fi

    die "Could not detect installed provider."
}

compose_up() {
    local provider="$1"

    docker compose -f "${COMPOSE_FILE}" \
        --project-directory "${INSTALL_DIR}" \
        --env-file "${ENV_FILE}" \
        up -d --force-recreate "${provider}"
}

get_container_id() {
    local provider="$1"

    docker ps -aq -f "name=^/${provider}$" | head -n1
}

create_backup_image() {
    local provider="$1"
    local container_id
    local image_id
    local backup_ref

    container_id="$(get_container_id "${provider}")"
    if [ -z "${container_id}" ]; then
        return 0
    fi

    image_id="$(docker inspect --format '{{.Image}}' "${container_id}" 2>/dev/null || true)"
    if [ -z "${image_id}" ]; then
        return 0
    fi

    backup_ref="mtproxy-installer/${provider}-backup:$(date -u +%Y%m%d%H%M%S)"
    docker image tag "${image_id}" "${backup_ref}" >/dev/null
    printf '%s\n' "${backup_ref}"
}

validate_mtg_config() {
    local image_ref="$1"
    local config_path="${INSTALL_DIR}/providers/mtg/mtg.conf"

    log "Validating mtg config with ${image_ref}"
    docker run --rm \
        -v "${config_path}:/config.toml:ro" \
        "${image_ref}" \
        access /config.toml >/dev/null
}

wait_for_telemt() {
    local api_port
    local health_url
    local users_url
    local attempt

    api_port="$(get_env_value "${ENV_FILE}" "API_PORT" "9091")"
    health_url="http://127.0.0.1:${api_port}/v1/health"
    users_url="http://127.0.0.1:${api_port}/v1/users"

    for attempt in $(seq 1 30); do
        if curl -fsS "${health_url}" >/dev/null 2>&1 && curl -fsS "${users_url}" >/dev/null 2>&1; then
            log "Telemt API is healthy"
            return 0
        fi

        sleep 2
    done

    return 1
}

wait_for_mtg() {
    local container_id
    local status
    local attempt

    for attempt in $(seq 1 30); do
        container_id="$(get_container_id "mtg")"
        if [ -n "${container_id}" ]; then
            status="$(docker inspect --format '{{.State.Status}}' "${container_id}" 2>/dev/null || true)"
            case "${status}" in
                running)
                    log "mtg container is running"
                    return 0
                ;;
                exited|dead)
                    return 1
                ;;
            esac
        fi

        sleep 2
    done

    return 1
}

validate_running_provider() {
    case "$1" in
        telemt) wait_for_telemt ;;
        mtg)    wait_for_mtg ;;
        *)      die "Unknown provider: $1" ;;
    esac
}

check_installation() {
    local provider="$1"
    local config_path

    if [ ! -d "${INSTALL_DIR}" ]; then
        die "Installation directory not found: ${INSTALL_DIR}"
    fi

    if [ ! -f "${ENV_FILE}" ]; then
        die "Environment file not found: ${ENV_FILE}"
    fi

    if [ ! -f "${COMPOSE_FILE}" ]; then
        die "Compose file not found: ${COMPOSE_FILE}"
    fi

    config_path="$(provider_config_path "${provider}")"
    if [ ! -f "${config_path}" ]; then
        die "Provider config not found: ${config_path}"
    fi
}

rollback_update() {
    local provider="$1"
    local image_var="$2"
    local rollback_image="$3"

    if [ -z "${rollback_image}" ]; then
        die "Update failed and no rollback image is available."
    fi

    log "Rolling back ${provider} to ${rollback_image}"
    set_env_value "${ENV_FILE}" "${image_var}" "${rollback_image}"
    compose_up "${provider}" >/dev/null

    if ! validate_running_provider "${provider}"; then
        die "Rollback failed. Manual recovery required."
    fi
}

show_result() {
    local provider="$1"
    local source_ref="$2"
    local image_ref="$3"

    log ""
    log "=============================="
    log "${provider} update complete"
    log "=============================="
    log ""
    log "Provider: ${provider}"
    log "Source:   ${source_ref}"
    log "Active:   ${image_ref}"

    case "${provider}" in
        telemt)
            log "Health:   curl http://127.0.0.1:$(get_env_value "${ENV_FILE}" "API_PORT" "9091")/v1/health"
        ;;
        mtg)
            log "Logs:     docker compose -f ${COMPOSE_FILE} --project-directory ${INSTALL_DIR} --env-file ${ENV_FILE} logs -f mtg"
        ;;
    esac
}

main() {
    local provider
    local image_var
    local source_var
    local source_ref
    local current_image
    local target_image
    local rollback_image
    local backup_image

    require_root

    provider="$(detect_provider)"
    check_installation "${provider}"

    image_var="$(provider_image_var "${provider}")"
    source_var="$(provider_source_var "${provider}")"

    source_ref="$(get_env_value "${ENV_FILE}" "${source_var}" "")"
    current_image="$(get_env_value "${ENV_FILE}" "${image_var}" "")"

    if [ -z "${source_ref}" ]; then
        if [ -n "${current_image}" ]; then
            source_ref="${current_image}"
        else
            source_ref="$(provider_default_source "${provider}")"
        fi
    fi

    log "=============================="
    log "MTProxy Provider Updater"
    log "=============================="
    log ""
    log "Install dir: ${INSTALL_DIR}"
    log "Provider: ${provider}"
    log "Configured source: ${source_ref}"
    log ""

    set_env_value "${ENV_FILE}" "PROVIDER" "${provider}"
    set_env_value "${ENV_FILE}" "${source_var}" "${source_ref}"

    backup_image="$(create_backup_image "${provider}" || true)"
    rollback_image="${current_image}"
    if [ -n "${backup_image}" ]; then
        rollback_image="${backup_image}"
        log "Prepared rollback image: ${rollback_image}"
    fi

    target_image="$(resolve_image_ref "${source_ref}")"

    if [ "${provider}" = "mtg" ]; then
        validate_mtg_config "${target_image}"
    fi

    if [ -n "${current_image}" ] && [ "${target_image}" = "${current_image}" ]; then
        log "Image is already up to date: ${current_image}"
        if ! validate_running_provider "${provider}"; then
            die "Current provider is not healthy. Update aborted."
        fi
        show_result "${provider}" "${source_ref}" "${current_image}"
        return 0
    fi

    set_env_value "${ENV_FILE}" "${image_var}" "${target_image}"

    if ! compose_up "${provider}" >/dev/null; then
        rollback_update "${provider}" "${image_var}" "${rollback_image}"
        die "Update failed while restarting the provider. Previous image restored."
    fi

    if ! validate_running_provider "${provider}"; then
        rollback_update "${provider}" "${image_var}" "${rollback_image}"
        die "Update failed validation. Previous image restored."
    fi

    show_result "${provider}" "${source_ref}" "${target_image}"
}

main "$@"
