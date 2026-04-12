#!/usr/bin/env bash

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/mtproxy-installer}"
KEEP_DATA="${KEEP_DATA:-false}"
ENV_FILE="${INSTALL_DIR}/.env"
COMPOSE_FILE="${INSTALL_DIR}/docker-compose.yml"
TELEMT_CONFIG="${INSTALL_DIR}/providers/telemt/telemt.toml"
MTG_CONFIG="${INSTALL_DIR}/providers/mtg/mtg.conf"
UNINSTALL_STRATEGY="telemt_only"
IMAGE_REF_PATTERN='^[A-Za-z0-9][A-Za-z0-9._/@:-]{0,254}$'
TELEMT_COMPOSE_MARKER="./providers/telemt/telemt.toml"
MTG_COMPOSE_MARKER="./providers/mtg/mtg.conf"

PROVIDER="unknown"
PROVIDER_SOURCE="unknown"
CLEANUP_STATUS="failed_preflight"
DATA_REMOVED="false"
IMAGE_CLEANUP="unknown"
OUTCOME=""
PRINTED_CONTEXT="false"
PRINTED_CLEANUP="false"
COMPOSE_STOPPED="false"

TELEMT_IMAGES=()
ALLOWED_INSTALL_ROOTS=("/opt")

log() {
    printf '%s\n' "$1"
}

warn() {
    printf 'WARN: %s\n' "$1"
}

error() {
    printf 'ERROR: %s\n' "$1" >&2
}

die() {
    error "$1"
    exit 1
}

refresh_install_paths() {
    ENV_FILE="${INSTALL_DIR}/.env"
    COMPOSE_FILE="${INSTALL_DIR}/docker-compose.yml"
    TELEMT_CONFIG="${INSTALL_DIR}/providers/telemt/telemt.toml"
    MTG_CONFIG="${INSTALL_DIR}/providers/mtg/mtg.conf"
}

normalize_install_dir_path() {
    local raw_path="$1"
    local normalized=""

    if command -v realpath >/dev/null 2>&1; then
        normalized="$(realpath -m -s -- "${raw_path}" 2>/dev/null || true)"
    fi
    if [ -z "${normalized}" ] && command -v readlink >/dev/null 2>&1; then
        normalized="$(readlink -m -- "${raw_path}" 2>/dev/null || true)"
    fi
    if [ -z "${normalized}" ]; then
        abort_uninstall "failed_preflight" "Could not normalize INSTALL_DIR path: ${raw_path}"
    fi

    printf '%s\n' "${normalized}"
}

resolve_canonical_path() {
    local raw_path="$1"
    local canonical=""

    if command -v realpath >/dev/null 2>&1; then
        canonical="$(realpath -e -- "${raw_path}" 2>/dev/null || true)"
    fi
    if [ -z "${canonical}" ] && command -v readlink >/dev/null 2>&1; then
        canonical="$(readlink -f -- "${raw_path}" 2>/dev/null || true)"
    fi
    if [ -z "${canonical}" ]; then
        abort_uninstall "failed_preflight" "Could not resolve canonical INSTALL_DIR path: ${raw_path}"
    fi

    printf '%s\n' "${canonical}"
}

path_has_symlink_component() {
    local target_path="$1"
    local current="/"
    local segment
    local segments=()

    if [ "${target_path}" = "/" ]; then
        return 1
    fi

    IFS='/' read -r -a segments <<< "${target_path#/}"
    for segment in "${segments[@]}"; do
        if [ -z "${segment}" ]; then
            continue
        fi
        current="${current%/}/${segment}"
        if [ -L "${current}" ]; then
            return 0
        fi
    done

    return 1
}

is_within_allowed_install_roots() {
    local target_path="$1"
    local root

    for root in "${ALLOWED_INSTALL_ROOTS[@]}"; do
        case "${target_path}" in
            "${root}"|"${root}/"*)
                return 0
            ;;
        esac
    done

    return 1
}

prepare_install_dir() {
    local normalized_path
    local canonical_path

    normalized_path="$(normalize_install_dir_path "${INSTALL_DIR}")"
    if path_has_symlink_component "${normalized_path}"; then
        abort_uninstall "failed_preflight" "INSTALL_DIR must not include symlink components: ${normalized_path}" "use a real directory path under /opt"
    fi
    if [ ! -d "${normalized_path}" ]; then
        abort_uninstall "failed_preflight" "Installation directory not found: ${normalized_path}"
    fi

    canonical_path="$(resolve_canonical_path "${normalized_path}")"
    if [ -L "${canonical_path}" ]; then
        abort_uninstall "failed_preflight" "INSTALL_DIR must not be a symlink target: ${canonical_path}" "use a non-symlink install directory"
    fi
    if ! is_within_allowed_install_roots "${canonical_path}"; then
        abort_uninstall "failed_preflight" "Refusing INSTALL_DIR outside allowed roots: ${canonical_path}" "allowed roots: ${ALLOWED_INSTALL_ROOTS[*]}"
    fi

    INSTALL_DIR="${canonical_path}"
    refresh_install_paths
}

print_context_markers() {
    if [ "${PRINTED_CONTEXT}" = "true" ]; then
        return
    fi

    log "Install dir: ${INSTALL_DIR}"
    log "Strategy: ${UNINSTALL_STRATEGY}"
    log "Provider: ${PROVIDER}"
    log "Keep data: ${KEEP_DATA}"
    PRINTED_CONTEXT="true"
}

print_cleanup_markers() {
    if [ "${PRINTED_CLEANUP}" = "true" ]; then
        return
    fi

    log "Cleanup status: ${CLEANUP_STATUS}"
    log "Data removed: ${DATA_REMOVED}"
    log "Image cleanup: ${IMAGE_CLEANUP}"
    if [ -n "${OUTCOME}" ]; then
        log "Outcome: ${OUTCOME}"
    fi
    PRINTED_CLEANUP="true"
}

abort_uninstall() {
    local status="$1"
    local message="$2"
    local hint="${3:-}"

    CLEANUP_STATUS="${status}"
    if [ "${IMAGE_CLEANUP}" = "unknown" ]; then
        IMAGE_CLEANUP="skipped"
    fi
    if [ -n "${hint}" ]; then
        log "Hint: ${hint}"
    fi
    OUTCOME="${message}"

    print_context_markers
    print_cleanup_markers
    die "${message}"
}

normalize_keep_data() {
    local normalized

    normalized="$(printf '%s' "${KEEP_DATA}" | tr '[:upper:]' '[:lower:]')"
    case "${normalized}" in
        true|false)
            KEEP_DATA="${normalized}"
        ;;
        *)
            abort_uninstall "failed_preflight" "KEEP_DATA must be 'true' or 'false', got '${KEEP_DATA}'"
        ;;
    esac
}

require_root() {
    if [ "${EUID}" -ne 0 ]; then
        abort_uninstall "failed_preflight" "Run as root. Example: curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/uninstall.sh | sudo bash"
    fi
}

check_installation() {
    if [ ! -d "${INSTALL_DIR}" ]; then
        abort_uninstall "failed_preflight" "Installation directory not found: ${INSTALL_DIR}"
    fi
}

validate_install_dir_for_removal() {
    case "${INSTALL_DIR}" in
        ""|"/"|"/opt"|"/usr"|"/var"|"/home"|"/tmp"|"/etc"|"/root")
            abort_uninstall "failed_preflight" "Refusing to remove protected INSTALL_DIR: ${INSTALL_DIR}"
        ;;
    esac
}

read_path_owner_uid() {
    local path="$1"
    local owner=""

    owner="$(stat -c '%u' -- "${path}" 2>/dev/null || true)"
    if [ -z "${owner}" ]; then
        owner="$(stat -f '%u' -- "${path}" 2>/dev/null || true)"
    fi

    printf '%s\n' "${owner}"
}

read_path_mode() {
    local path="$1"
    local mode=""

    mode="$(stat -c '%a' -- "${path}" 2>/dev/null || true)"
    if [ -z "${mode}" ]; then
        mode="$(stat -f '%Lp' -- "${path}" 2>/dev/null || true)"
    fi

    printf '%s\n' "${mode}"
}

enforce_root_owned_non_writable() {
    local path="$1"
    local label="$2"
    local owner_uid
    local mode_octal

    owner_uid="$(read_path_owner_uid "${path}")"
    if [ -z "${owner_uid}" ]; then
        abort_uninstall "failed_preflight" "Could not determine owner for ${label}: ${path}"
    fi
    if [ "${owner_uid}" != "0" ]; then
        abort_uninstall "failed_preflight" "${label} must be root-owned (uid=0), got uid=${owner_uid}: ${path}"
    fi

    mode_octal="$(read_path_mode "${path}")"
    if [ -z "${mode_octal}" ]; then
        abort_uninstall "failed_preflight" "Could not determine mode for ${label}: ${path}"
    fi
    if ! printf '%s' "${mode_octal}" | grep -Eq '^[0-7]{3,4}$'; then
        abort_uninstall "failed_preflight" "Unsupported mode format for ${label}: ${path} (${mode_octal})"
    fi
    if (( (8#${mode_octal}) & 8#022 )); then
        abort_uninstall "failed_preflight" "${label} must not be group/other-writable (mode=${mode_octal}): ${path}"
    fi
}

require_runtime_file() {
    local path="$1"
    local label="$2"

    if [ ! -e "${path}" ]; then
        abort_uninstall "failed_preflight" "${label} not found: ${path}"
    fi
    if [ -L "${path}" ]; then
        abort_uninstall "failed_preflight" "${label} must not be a symlink: ${path}"
    fi
    if [ ! -f "${path}" ]; then
        abort_uninstall "failed_preflight" "${label} path is not a file: ${path}"
    fi
}

provider_config_path_for() {
    local provider="$1"

    case "${provider}" in
        telemt)
            printf '%s\n' "${TELEMT_CONFIG}"
        ;;
        mtg)
            printf '%s\n' "${MTG_CONFIG}"
        ;;
        *)
            printf '%s\n' ""
        ;;
    esac
}

compose_marker_for_provider() {
    local provider="$1"

    case "${provider}" in
        telemt)
            printf '%s\n' "${TELEMT_COMPOSE_MARKER}"
        ;;
        mtg)
            printf '%s\n' "${MTG_COMPOSE_MARKER}"
        ;;
        *)
            printf '%s\n' ""
        ;;
    esac
}

validate_runtime_preflight_contract() {
    local provider_config_path
    local provider_marker

    require_runtime_file "${ENV_FILE}" "Runtime env file"
    require_runtime_file "${COMPOSE_FILE}" "Runtime compose file"

    provider_config_path="$(provider_config_path_for "${PROVIDER}")"
    if [ -z "${provider_config_path}" ]; then
        abort_uninstall "failed_preflight" "Unsupported provider '${PROVIDER}' for runtime contract validation"
    fi
    require_runtime_file "${provider_config_path}" "Provider config file"

    provider_marker="$(compose_marker_for_provider "${PROVIDER}")"
    if [ -z "${provider_marker}" ]; then
        abort_uninstall "failed_preflight" "Missing compose marker contract for provider '${PROVIDER}'"
    fi
    if ! grep -Fq "${provider_marker}" "${COMPOSE_FILE}"; then
        abort_uninstall "blocked_provider_mismatch" "Compose provider marker mismatch: expected '${provider_marker}' for provider '${PROVIDER}'" "restore the matching provider compose contract before uninstall"
    fi

    enforce_root_owned_non_writable "${INSTALL_DIR}" "Install directory"
    enforce_root_owned_non_writable "${ENV_FILE}" "Runtime env file"
    enforce_root_owned_non_writable "${COMPOSE_FILE}" "Runtime compose file"
    enforce_root_owned_non_writable "${provider_config_path}" "Provider config file"
}

is_valid_image_ref() {
    local value="$1"

    if [ -z "${value}" ]; then
        return 1
    fi
    if [ "${value#-}" != "${value}" ]; then
        return 1
    fi
    if [[ "${value}" == *".."* ]]; then
        return 1
    fi
    if printf '%s' "${value}" | grep -Eq "${IMAGE_REF_PATTERN}"; then
        return 0
    fi

    return 1
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

detect_provider() {
    local configured
    local telemt_exists="false"
    local mtg_exists="false"
    local runtime_provider="unknown"

    if [ -f "${TELEMT_CONFIG}" ]; then
        telemt_exists="true"
    fi
    if [ -f "${MTG_CONFIG}" ]; then
        mtg_exists="true"
    fi

    if [ "${telemt_exists}" = "true" ] && [ "${mtg_exists}" = "false" ]; then
        runtime_provider="telemt"
    fi
    if [ "${mtg_exists}" = "true" ] && [ "${telemt_exists}" = "false" ]; then
        runtime_provider="mtg"
    fi
    if [ "${telemt_exists}" = "true" ] && [ "${mtg_exists}" = "true" ]; then
        runtime_provider="ambiguous"
    fi

    configured="$(get_env_value "${ENV_FILE}" "PROVIDER" "")"
    configured="$(printf '%s' "${configured}" | tr '[:upper:]' '[:lower:]')"

    case "${configured}" in
        ""|telemt|mtg|official)
        ;;
        *)
            abort_uninstall "failed_preflight" "Unsupported PROVIDER value in ${ENV_FILE}: ${configured}" "set PROVIDER to telemt, mtg, or official"
        ;;
    esac

    if [ "${runtime_provider}" = "ambiguous" ]; then
        PROVIDER="unknown"
        abort_uninstall "blocked_ambiguous_provider" "Provider detection is ambiguous: both telemt and mtg configs exist" "keep exactly one provider config under ${INSTALL_DIR}/providers"
    fi

    if [ -n "${configured}" ]; then
        if [ "${runtime_provider}" = "unknown" ] && [ "${configured}" != "official" ]; then
            PROVIDER="${configured}"
            abort_uninstall "failed_preflight" "Provider '${configured}' declared in ${ENV_FILE} but provider config file is missing" "restore provider config file or fix PROVIDER value"
        fi
        if [ "${runtime_provider}" != "unknown" ] && [ "${configured}" != "${runtime_provider}" ]; then
            PROVIDER="${configured}"
            abort_uninstall "blocked_provider_mismatch" "Provider mismatch: env declares ${configured} but runtime config indicates ${runtime_provider}" "align PROVIDER in ${ENV_FILE} with provider config files"
        fi
        PROVIDER="${configured}"
        PROVIDER_SOURCE="env"
        return 0
    fi

    if [ "${runtime_provider}" = "unknown" ]; then
        abort_uninstall "failed_preflight" "Could not detect installed provider from ${ENV_FILE}, ${TELEMT_CONFIG}, or ${MTG_CONFIG}"
    fi

    PROVIDER="${runtime_provider}"
    PROVIDER_SOURCE="heuristic"
}

enforce_strategy_contract() {
    if [ "${PROVIDER}" != "telemt" ]; then
        abort_uninstall "blocked_unsupported_provider" "Unsupported provider '${PROVIDER}': uninstall strategy '${UNINSTALL_STRATEGY}' supports telemt only" "run manual cleanup for non-telemt runtime"
    fi
}

mark_partial_cleanup() {
    CLEANUP_STATUS="partial"
    OUTCOME="Telemt cleanup finished with partial removal"
}

stop_container() {
    if [ ! -f "${COMPOSE_FILE}" ]; then
        warn "Compose file not found: ${COMPOSE_FILE}. Skipping compose down."
        mark_partial_cleanup
        return 1
    fi

    warn "Destructive step: docker compose down --remove-orphans"
    if ! docker compose -f "${COMPOSE_FILE}" --project-directory "${INSTALL_DIR}" --env-file "${ENV_FILE}" down --remove-orphans >/dev/null 2>&1; then
        mark_partial_cleanup
        error "docker compose down failed for ${COMPOSE_FILE}"
        return 1
    fi

    COMPOSE_STOPPED="true"
    log "Hint: compose stack stopped"
    return 0
}

add_telemt_image_candidate() {
    local candidate="$1"
    local existing

    if [ -z "${candidate}" ]; then
        return
    fi

    for existing in "${TELEMT_IMAGES[@]}"; do
        if [ "${existing}" = "${candidate}" ]; then
            return
        fi
    done

    TELEMT_IMAGES+=("${candidate}")
}

collect_telemt_images() {
    local composed_images
    local image

    TELEMT_IMAGES=()

    composed_images="$(docker compose -f "${COMPOSE_FILE}" --project-directory "${INSTALL_DIR}" --env-file "${ENV_FILE}" config --images 2>/dev/null || true)"
    while IFS= read -r image; do
        image="$(printf '%s' "${image}" | tr -d '[:space:]')"
        if [ -z "${image}" ]; then
            continue
        fi
        if ! is_valid_image_ref "${image}"; then
            abort_uninstall "failed_preflight" "Compose returned invalid image reference for telemt runtime: ${image}" "fix image references in ${COMPOSE_FILE} and ${ENV_FILE}"
        fi
        add_telemt_image_candidate "${image}"
    done <<< "${composed_images}"
}

remove_image() {
    local image
    local removed_any="false"
    local failed_any="false"

    collect_telemt_images
    IMAGE_CLEANUP="not_found"

    if [ "${#TELEMT_IMAGES[@]}" -eq 0 ]; then
        warn "No telemt runtime images detected from compose contract"
        return
    fi

    warn "Destructive step: removing telemt image candidates"
    for image in "${TELEMT_IMAGES[@]}"; do
        if ! is_valid_image_ref "${image}"; then
            failed_any="true"
            error "Skipping invalid image reference candidate: ${image}"
            continue
        fi

        if ! docker image inspect -- "${image}" >/dev/null 2>&1; then
            continue
        fi

        if docker rmi -- "${image}" >/dev/null 2>&1; then
            removed_any="true"
            log "Hint: removed image ${image}"
            continue
        fi

        failed_any="true"
        error "Failed to remove image ${image}"
    done

    if [ "${failed_any}" = "true" ]; then
        IMAGE_CLEANUP="failed"
        mark_partial_cleanup
        return
    fi

    if [ "${removed_any}" = "true" ]; then
        IMAGE_CLEANUP="removed"
        return
    fi

    IMAGE_CLEANUP="not_found"
}

remove_data() {
    if [ "${KEEP_DATA}" = "true" ]; then
        DATA_REMOVED="false"
        log "Hint: keeping installation directory (KEEP_DATA=true)"
        return
    fi
    if [ "${COMPOSE_STOPPED}" != "true" ]; then
        DATA_REMOVED="false"
        mark_partial_cleanup
        error "Refusing to remove installation directory because compose teardown did not succeed"
        return
    fi

    warn "Destructive step: removing install directory ${INSTALL_DIR}"
    if rm -rf "${INSTALL_DIR}"; then
        DATA_REMOVED="true"
        return
    fi

    DATA_REMOVED="false"
    mark_partial_cleanup
    error "Failed to remove installation directory ${INSTALL_DIR}"
}

finalize_cleanup_status() {
    if [ "${CLEANUP_STATUS}" = "partial" ]; then
        return
    fi

    if [ "${KEEP_DATA}" = "true" ]; then
        CLEANUP_STATUS="completed_keep_data"
        OUTCOME="Telemt runtime removed; install directory preserved"
        return
    fi

    CLEANUP_STATUS="completed"
    OUTCOME="Telemt runtime removed; install directory deleted"
}

print_banner() {
    log "=============================="
    log "MTProxy Uninstaller (v1 telemt-only)"
    log "=============================="
    log ""
}

main() {
    normalize_keep_data
    require_root
    prepare_install_dir
    check_installation

    print_banner
    detect_provider
    print_context_markers
    log "Hint: provider detection source is ${PROVIDER_SOURCE}"

    validate_runtime_preflight_contract
    enforce_strategy_contract
    validate_install_dir_for_removal

    warn "Destructive action requested: stop runtime, remove images, and apply KEEP_DATA policy"
    CLEANUP_STATUS="in_progress"

    if stop_container; then
        remove_image
        remove_data
    else
        IMAGE_CLEANUP="skipped"
        DATA_REMOVED="false"
        log "Hint: compose teardown failed; skipping image and data removal"
    fi
    finalize_cleanup_status

    print_cleanup_markers

    if [ "${CLEANUP_STATUS}" = "partial" ]; then
        die "Uninstall completed with partial cleanup. Review markers above."
    fi

    log ""
    log "=============================="
    log "Uninstall complete"
    log "=============================="
    log ""
    log "To reinstall, run:"
    log "curl -fsSL https://raw.githubusercontent.com/ichinya/mtproxy-installer/main/install.sh | sudo bash"
}

main "$@"
