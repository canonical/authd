#!/usr/bin/env bash
# authd Development Environment Manager
#
# Creates and manages an LXD VM for authd development.
# The VM runs full systemd + D-Bus + SSH + GDM, with the host source
# tree bind-mounted for live editing. Suitable for building, testing,
# and integration testing (SSH, TTY, and GDM login via PAM/NSS).
#
# Usage: ./dev/dev-env.sh <command> [options]
# Run './dev/dev-env.sh help' for details.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/common.sh"

PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Defaults (overridable via global flags — see 'help')
CONTAINER_NAME="authd-dev"
RELEASE="noble"
PROFILE_NAME="${CONTAINER_NAME}"
WORKSPACE_PATH="/workspace/authd"

# --- Helpers ---

container_exists() {
    lxc info "$CONTAINER_NAME" &>/dev/null
}

container_running() {
    local state
    state=$(get_container_status)
    [[ "$state" == "RUNNING" || "$state" == "Running" ]]
}

get_container_status() {
    lxc info "$CONTAINER_NAME" 2>/dev/null | awk '/^Status:/ {print $2}'
}

get_container_ip() {
    lxc list "$CONTAINER_NAME" --format csv -c4 2>/dev/null \
        | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1
}

wait_for_ip() {
    local max_wait=60 waited=0
    info "Waiting for VM network..." >&2
    while [[ $waited -lt $max_wait ]]; do
        local ip
        ip=$(get_container_ip)
        if [[ -n "$ip" ]]; then
            echo "$ip"
            return 0
        fi
        sleep 2
        waited=$((waited + 2))
    done
    die "Timed out waiting for VM IP address"
}

# Run a command as the ubuntu user inside the VM.
exec_in_vm() {
    lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c "$1"
}

# --- LXD Profile ---

ensure_profile() {
    if lxc profile show "$PROFILE_NAME" &>/dev/null; then
        info "Updating LXD profile '${PROFILE_NAME}'..."
    else
        info "Creating LXD profile '${PROFILE_NAME}'..."
        lxc profile create "$PROFILE_NAME"
    fi

    cat <<EOF | lxc profile edit "$PROFILE_NAME"
config:
  limits.memory: 4GB
  limits.cpu: "4"
devices:
  authd-src:
    type: disk
    source: "${PROJECT_DIR}"
    path: "${WORKSPACE_PATH}"
EOF

    ok "Profile '${PROFILE_NAME}' configured (4GB RAM, 4 vCPU, +4GB swap via cloud-init)"
}

# --- Cloud-Init ---

# Extract Go toolchain version from go.mod (e.g., "go1.25.8" -> "1.25.8").
get_go_version() {
    local toolchain_line
    toolchain_line=$(grep '^toolchain go' "${PROJECT_DIR}/go.mod" 2>/dev/null || true)
    if [[ -n "$toolchain_line" ]]; then
        echo "${toolchain_line#toolchain go}"
        return 0
    fi
    # Fallback to "go X.Y.Z" directive
    local go_line
    go_line=$(grep '^go ' "${PROJECT_DIR}/go.mod" 2>/dev/null | head -1)
    [[ -n "$go_line" ]] || die "Cannot determine Go version from go.mod"
    echo "$go_line" | awk '{print $2}'
}

# Extract Rust channel from rust-toolchain.toml (e.g., "1.94.0").
get_rust_channel() {
    local toml="${PROJECT_DIR}/authd-oidc-brokers/rust-toolchain.toml"
    if [[ -f "$toml" ]]; then
        local channel
        channel=$(sed -n 's/^channel = "\(.*\)"/\1/p' "$toml" | head -1)
        if [[ -n "$channel" ]]; then
            echo "$channel"
            return 0
        fi
    fi
    echo "stable"
}

generate_cloud_init() {
    local ssh_key_file ssh_key go_version rust_channel vm_password
    ssh_key_file=$(detect_ssh_key)
    ssh_key=$(cat "$ssh_key_file")
    go_version=$(get_go_version)
    rust_channel=$(get_rust_channel)
    vm_password=$(head -c 16 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 16)

    # Save the VM password to a file for later retrieval, rather than only
    # printing it to the terminal where it persists in scrollback history.
    local pw_dir="${HOME}/.config/${CONTAINER_NAME}"
    mkdir -p "$pw_dir"
    printf '%s\n' "$vm_password" > "${pw_dir}/vm-password"
    chmod 600 "${pw_dir}/vm-password"

    info "Using SSH key: ${ssh_key_file}" >&2
    info "Go version:    ${go_version} (from go.mod)" >&2
    info "Rust channel:  ${rust_channel} (from rust-toolchain.toml)" >&2
    info "VM password:   saved to ${pw_dir}/vm-password (for GDM console login)" >&2

    # Use awk for safe placeholder replacement (no sed metacharacter issues).
    # Sanitize values for awk gsub: '&' means "matched text" and '\' is
    # an escape in replacement strings, so they must be escaped first.
    ssh_key=$(printf '%s' "$ssh_key" | sed -e 's/[\&]/\\&/g')

    awk \
        -v ssh_key="$ssh_key" \
        -v go_ver="$go_version" \
        -v rust_ch="$rust_channel" \
        -v vm_pw="$vm_password" \
        '{
            gsub(/__SSH_PUBLIC_KEY__/, ssh_key)
            gsub(/__GO_VERSION__/, go_ver)
            gsub(/__RUST_CHANNEL__/, rust_ch)
            gsub(/__VM_PASSWORD__/, vm_pw)
            print
        }' "${SCRIPT_DIR}/cloud-init.yaml"
}

# --- Commands ---

cmd_up() {
    # Reject any unknown arguments (global flags are parsed before dispatch)
    if [[ $# -gt 0 ]]; then
        die "Unknown argument(s): $*. Global flags (--name, --release, --workspace) must come before the subcommand."
    fi

    # Preflight
    command -v lxc &>/dev/null || die "LXD not installed. Install: sudo snap install lxd && lxd init --auto"
    [[ -f "${SCRIPT_DIR}/cloud-init.yaml" ]] || die "Missing ${SCRIPT_DIR}/cloud-init.yaml"

    if container_exists; then
        if container_running; then
            ok "VM '${CONTAINER_NAME}' is already running"
            info "IP: $(get_container_ip)"
            info "Connect:  ./dev/dev-env.sh shell  or  ./dev/dev-env.sh ssh"
            info "Run cmd:  ./dev/dev-env.sh exec <command>"
            info "Logs:     ./dev/dev-env.sh logs authd"
            return 0
        else
            info "Starting existing VM '${CONTAINER_NAME}'..."
            lxc start "$CONTAINER_NAME"
            local ip
            ip=$(wait_for_ip)
            ok "VM started — IP: ${ip}"
            return 0
        fi
    fi

    printf '\n%s\n' "${BOLD}Creating authd development environment${NC}"
    echo "  VM:         ${CONTAINER_NAME}"
    echo "  Image:      ubuntu:${RELEASE}"
    echo "  Source:      ${PROJECT_DIR} → ${WORKSPACE_PATH}"
    echo ""

    # 1. Create LXD profile
    ensure_profile

    # 2. Generate cloud-init with SSH key
    local cloud_init
    cloud_init=$(generate_cloud_init)

    # 3. Initialize VM (don't start yet — need to set cloud-init first)
    info "Initializing VM from ubuntu:${RELEASE}..."
    lxc init "ubuntu:${RELEASE}" "$CONTAINER_NAME" --vm \
        --profile default \
        --profile "$PROFILE_NAME"

    # 3a. Resize root disk before starting — the default 10GiB is far too small.
    #     Space needed: ~2GB base + ~600MB Go + ~2GB Rust + ~400MB GDM + ~8GB build/module caches.
    #     40GiB provides comfortable headroom for iterative builds and snapshots.
    info "Sizing root disk to 40GiB"
    lxc config device override "$CONTAINER_NAME" root size=40GiB
    ok "Root disk sized to 40GiB (swap space enabled to prevent OOM during large builds)"

    # 4. Inject cloud-init user-data
    info "Applying cloud-init configuration..."
    lxc config set "$CONTAINER_NAME" user.user-data - <<< "$cloud_init"

    # 5. Start
    info "Starting VM..."
    lxc start "$CONTAINER_NAME"

    # 6. Wait for network
    local ip
    ip=$(wait_for_ip)
    ok "VM started — IP: ${ip}"

    # 7. Wait for cloud-init provisioning
    info "Waiting for cloud-init provisioning (takes 5-15 min on first run)..."
    echo "  Tail logs: lxc exec ${CONTAINER_NAME} -- tail -f /var/log/cloud-init-output.log"
    echo ""
    if lxc exec "$CONTAINER_NAME" -- cloud-init status --wait 2>/dev/null; then
        ok "Cloud-init provisioning complete"
    else
        warn "Cloud-init finished with errors"
        warn "Check: lxc exec ${CONTAINER_NAME} -- cat /var/log/cloud-init-output.log"
    fi

    # 8. Verify installed tools
    echo ""
    info "Verifying toolchain:"
    # shellcheck disable=SC2016  # Single quotes intentional: commands run inside the VM
    {
    lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c \
        'echo "  Go:      $(go version 2>/dev/null || echo NOT FOUND)"'
    lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c \
        'echo "  Rust:    $(rustc --version 2>/dev/null || echo NOT FOUND)"'
    lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c \
        'echo "  Cargo:   $(cargo --version 2>/dev/null || echo NOT FOUND)"'
    lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c \
        'echo "  Protoc:  $(protoc --version 2>/dev/null || echo NOT FOUND)"'
    }

    # 9. Create 'clean' snapshot (toolchain only, before build)
    echo ""
    info "Creating 'clean' snapshot..."
    lxc snapshot "$CONTAINER_NAME" clean
    ok "Snapshot 'clean' created (toolchain only — before authd build)"

    # 10. Build and install authd (PAM, NSS, systemd config)
    echo ""
    info "Building and installing authd (daemon, PAM, NSS)..."
    echo "  This mirrors the steps in CONTRIBUTING.md and debian/install."
    echo ""
    if lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c "cd ${WORKSPACE_PATH} && ./dev/scripts/install-authd --all"; then
        ok "authd built and installed"

        # Create 'installed' snapshot with fully working authd stack
        echo ""
        info "Creating 'installed' snapshot..."
        lxc snapshot "$CONTAINER_NAME" installed
        ok "Snapshot 'installed' created"
    else
        warn "authd build failed — the VM is still usable with toolchain installed"
        warn "Debug: ./dev/dev-env.sh logs cloud-init"
        warn "Retry: ./dev/dev-env.sh exec ./dev/scripts/install-authd --all"
    fi

    # Print summary
    echo ""
    printf '%s\n' "${GREEN}${BOLD} Development environment ready!${NC}"
    echo ""
    printf '%s\n' "${BOLD}Activate a broker:${NC}"
    echo "  Google:     ./dev/dev-env.sh broker google --client-id ID --client-secret SEC"
    echo "  Entra ID:   ./dev/dev-env.sh broker msentraid --issuer URL --client-id ID"
    echo "  Edit conf:  ./dev/dev-env.sh broker google conf    # Edit /etc/authd-<variant>/broker.conf and restart"
    echo ""
    printf '%s\n' "${BOLD}Development:${NC}"
    echo "  ./dev/dev-env.sh test                     # Run all tests with race detection"
    echo "  ./dev/dev-env.sh build authd              # Rebuild daemon (fast)"
    echo "  ./dev/dev-env.sh build pam                # Rebuild PAM modules"
    echo "  ./dev/dev-env.sh build all                # Full reinstall"
    echo "  ./dev/dev-env.sh logs authd               # Tail authd logs"
    echo ""
    printf '%s\n' "${BOLD}Connect:${NC}"
    echo "  ./dev/dev-env.sh shell                    # Direct shell (no PAM, always works)"
    echo "  ./dev/dev-env.sh ssh                      # SSH (goes through PAM — for login testing)"
    echo ""
    printf '%s\n' "${BOLD}Snapshots:${NC}"
    echo "  ./dev/dev-env.sh restore clean            # Reset to toolchain only (pre-build)"
    echo "  ./dev/dev-env.sh restore installed        # Reset to freshly built authd"
    echo "  ./dev/dev-env.sh snapshot <name>          # Save current state"
    echo ""
}

cmd_down() {
    local force=false
    [[ "${1:-}" == "--force" || "${1:-}" == "-f" ]] && force=true

    if ! container_exists; then
        warn "VM '${CONTAINER_NAME}' does not exist"
        return 0
    fi

    if $force; then
        info "Force-removing VM '${CONTAINER_NAME}'..."
        lxc delete "$CONTAINER_NAME" --force 2>/dev/null || true
    else
        if container_running; then
            info "Stopping VM '${CONTAINER_NAME}'..."
            lxc stop "$CONTAINER_NAME"
        fi
        info "Deleting VM '${CONTAINER_NAME}'..."
        lxc delete "$CONTAINER_NAME"
    fi

    # Clean up profile
    if lxc profile show "$PROFILE_NAME" &>/dev/null; then
        lxc profile delete "$PROFILE_NAME" 2>/dev/null || true
    fi

    ok "VM and profile removed"
}

cmd_stop() {
    if ! container_exists; then
        warn "VM '${CONTAINER_NAME}' does not exist"
        return 0
    fi
    if ! container_running; then
        ok "VM '${CONTAINER_NAME}' is already stopped"
        return 0
    fi
    info "Stopping VM '${CONTAINER_NAME}'..."
    lxc stop "$CONTAINER_NAME"
    ok "VM stopped — run './dev/dev-env.sh up' to restart"
}

cmd_shell() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"
    exec lxc exec -t "$CONTAINER_NAME" -- bash -l -c "PROMPT_COMMAND='PS1=\"\[\e[32;1m\][${CONTAINER_NAME} | Shell (No PAM)]\[\e[0m\] \u@\h:\w$ \"' exec su - ubuntu"
}

cmd_ssh() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"
    local ip
    ip=$(get_container_ip)
    [[ -n "$ip" ]] || die "Cannot determine VM IP"

    local ssh_key_private
    ssh_key_private=$(get_ssh_private_key)

    info "Note: Using SSH goes through PAM modules for integration testing."
    exec ssh -i "$ssh_key_private" \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o LogLevel=ERROR \
        "ubuntu@${ip}"
}

cmd_status() {
    if [[ "${1:-}" == "--deep" ]]; then
        cmd_validate
        return 0
    fi
    if ! container_exists; then
        echo "VM '${CONTAINER_NAME}': not created"
        echo "  Run: ./dev/dev-env.sh up"
        return 0
    fi

    printf '%s\n' "${BOLD}VM: ${CONTAINER_NAME}${NC}"
    local state
    state=$(get_container_status)
    echo "  Status:  ${state}"

    if [[ "$state" == "RUNNING" || "$state" == "Running" ]]; then
        local ip
        ip=$(get_container_ip)
        echo "  IP:      ${ip}"
    fi

    echo ""
    printf '%s\n' "${BOLD}Snapshots:${NC}"
    local snapshots
    snapshots=$(lxc info "$CONTAINER_NAME" 2>/dev/null | awk '/^Snapshots:/,0' | tail -n +2)
    if [[ -z "$snapshots" || "$snapshots" == *"Snapshots: []"* ]]; then
        echo "  (none)"
    else
        while IFS= read -r line; do printf '  %s\n' "$line"; done <<< "$snapshots"
    fi
}

cmd_snapshot() {
    local name="${1:-}"
    [[ -n "$name" ]] || die "Usage: ./dev/dev-env.sh snapshot <name>"
    container_exists || die "VM '${CONTAINER_NAME}' does not exist"

    info "Creating snapshot '${name}'..."
    lxc snapshot "$CONTAINER_NAME" "$name"
    ok "Snapshot '${name}' created"
}

cmd_restore() {
    local name="${1:-}"
    [[ -n "$name" ]] || die "Usage: ./dev/dev-env.sh restore <name>"
    container_exists || die "VM '${CONTAINER_NAME}' does not exist"

    info "Restoring snapshot '${name}'..."
    lxc restore "$CONTAINER_NAME" "$name"

    if ! container_running; then
        info "Starting container..."
        lxc start "$CONTAINER_NAME"
    fi

    local ip
    ip=$(wait_for_ip)
    ok "Restored '${name}' — IP: ${ip}"
}

cmd_broker() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"
    if [[ $# -lt 1 ]]; then
        cat <<EOF
${BOLD}Usage:${NC} ./dev/dev-env.sh broker <variant> [options]
       ./dev/dev-env.sh broker <variant> edit       # Open broker.conf in \$EDITOR and restart

${BOLD}Variants:${NC}  google, msentraid, oidc

${BOLD}Credential options:${NC}
  --client-id ID         OAuth2 client ID
  --client-secret SEC    OAuth2 client secret
  --issuer URL           OIDC issuer (required for msentraid; defaults for google)
  --ssh-suffixes LIST    Domains allowed for first-time SSH login (e.g. '@gmail.com')
                         Default: '*' (any domain) — restrict in production
  --allowed-users LIST   OWNER (DEFAULT), ALL, or usernames
  --rebuild              Force full binary rebuild (skip for credential updates)

${BOLD}Behaviour:${NC}
  If the broker is already installed, only credentials are updated (no rebuild).
  Pass --rebuild to recompile the binary after source changes.

${BOLD}Examples:${NC}
  ./dev/dev-env.sh broker google \\
      --client-id YOUR_ID --client-secret YOUR_SECRET

  ./dev/dev-env.sh broker msentraid \\
      --issuer https://login.microsoftonline.com/TENANT/v2.0 \\
      --client-id YOUR_ID

  ./dev/dev-env.sh broker google edit   # Open /etc/authd-google/broker.conf and restart
EOF
        return 0
    fi

    local variant="$1"
    shift

    # Validate variant immediately so typos give a clear error before any lxc exec.
    case "$variant" in
        google|msentraid|oidc) ;;
        *) die "Unknown variant: ${variant}. Use: google, msentraid, or oidc" ;;
    esac

    if [[ "${1:-}" == "edit" ]]; then
        local conf_file="/etc/authd-${variant}/broker.conf"
        local check_err
        if ! check_err=$(lxc exec "$CONTAINER_NAME" -- test -f "$conf_file" 2>&1); then
            if echo "$check_err" | grep -qi "agent"; then
                die "LXD VM agent is not ready — the VM may still be booting. Wait a moment and retry."
            fi
            die "Broker '${variant}' is not installed. Run: ./dev/dev-env.sh broker ${variant} --client-id ID --client-secret SEC"
        fi
        info "Opening ${conf_file}..."
        lxc exec "$CONTAINER_NAME" -t -- bash -c "sudo \${EDITOR:-nano} \"$conf_file\""
        info "Restarting authd-${variant} broker service..."
        if lxc exec "$CONTAINER_NAME" -- sudo systemctl restart "authd-${variant}"; then
            ok "Broker restarted."
        else
            warn "Failed to restart broker. Check: ./dev/dev-env.sh logs ${variant}"
        fi
        return 0
    fi

    # ssh_allowed_suffixes_first_auth must be set in broker.conf for new users to
    # log in via SSH for the first time. Default to '*' (any domain) when the dev
    # doesn't specify --ssh-suffixes, since this is a dev environment and you want
    # to be able to test SSH login without restricting to a specific domain.
    local has_rebuild=false
    local has_ssh_suffixes=false
    for arg in "$@"; do
        [[ "$arg" == "--rebuild" ]]      && has_rebuild=true
        [[ "$arg" == "--ssh-suffixes" ]] && has_ssh_suffixes=true
    done
    if ! $has_rebuild && ! $has_ssh_suffixes; then
        info "No --ssh-suffixes given; defaulting to '*' (all domains allowed for first-time SSH)."
        info "Pass --ssh-suffixes '@yourdomain.com' to restrict to a specific domain."
        set -- "$@" --ssh-suffixes '*'
    fi

    local args
    args=$(printf ' %q' "$@")

    # Build and configure (install-broker auto-detects reconfigure vs full install)
    lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c \
        "cd ${WORKSPACE_PATH} && ./dev/scripts/install-broker ${variant}${args}"
}

cmd_ip() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"
    get_container_ip
}

cmd_validate() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"

    local failures=0

    printf '\n%s\n\n' "${BOLD}Validating authd stack in ${CONTAINER_NAME}${NC}"

    # Use array for safe command construction
    _exec() { lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c "$1" 2>/dev/null; }
    _exec_root() { lxc exec "$CONTAINER_NAME" -- bash -lc "$1" 2>/dev/null; }

    # 1. Toolchain
    info "Toolchain:"
    for tool in "go version" "rustc --version" "cargo --version" "protoc --version"; do
        if _exec "$tool" >/dev/null 2>&1; then
            ok "  $tool"
        else
            error "  $tool: NOT FOUND"; failures=$((failures + 1))
        fi
    done

    # 2. authd socket
    echo ""
    info "authd:"
    if _exec_root "systemctl is-active --quiet authd.socket"; then
        ok "  authd.socket is active"
    else
        error "  authd.socket is NOT active"; failures=$((failures + 1))
    fi

    if _exec_root "test -S /run/authd.sock"; then
        ok "  /run/authd.sock exists"
    else
        info "  /run/authd.sock not yet created (created on first connection)"
    fi

    # 3. PAM module
    echo ""
    info "PAM:"
    local pam_dir
    pam_dir=$(_exec "dpkg-architecture -qDEB_HOST_MULTIARCH 2>/dev/null || gcc -dumpmachine 2>/dev/null || echo x86_64-linux-gnu" 2>/dev/null)
    pam_dir="${pam_dir:-x86_64-linux-gnu}"
    if _exec_root "test -f /usr/lib/${pam_dir}/security/pam_authd_exec.so"; then
        ok "  pam_authd_exec.so installed"
    else
        error "  pam_authd_exec.so NOT found"; failures=$((failures + 1))
    fi
    if _exec_root "grep -q authd /usr/share/pam-configs/authd 2>/dev/null"; then
        ok "  PAM config registered"
    else
        error "  PAM config NOT registered"; failures=$((failures + 1))
    fi

    # 4. NSS module
    echo ""
    info "NSS:"
    if _exec_root "test -f /usr/lib/${pam_dir}/libnss_authd.so.2"; then
        ok "  libnss_authd.so.2 installed"
    else
        error "  libnss_authd.so.2 NOT found"; failures=$((failures + 1))
    fi
    if _exec_root "grep -q authd /etc/nsswitch.conf"; then
        ok "  nsswitch.conf configured"
    else
        error "  nsswitch.conf NOT configured"; failures=$((failures + 1))
    fi

    # 5. Brokers
    echo ""
    info "Brokers:"
    local broker_count
    broker_count=$(_exec_root "ls /etc/authd/brokers.d/*.conf 2>/dev/null | wc -l" || echo "0")
    if [[ "$broker_count" -gt 0 ]]; then
        ok "  ${broker_count} broker(s) configured in /etc/authd/brokers.d/"
        _exec "authctl list brokers 2>/dev/null" | sed 's/^/    /' || true
    else
        info "  No brokers configured (run ./dev/dev-env.sh broker <variant> to add one)"
    fi

    # 6. SSH config
    echo ""
    info "SSH:"
    if _exec_root "test -f /etc/ssh/sshd_config.d/authd.conf"; then
        ok "  authd SSH config installed"
    else
        error "  authd SSH config NOT found"; failures=$((failures + 1))
    fi
    if _exec_root "systemctl is-active --quiet ssh"; then
        ok "  sshd is running"
    else
        error "  sshd is NOT running"; failures=$((failures + 1))
    fi

    echo ""
    if [[ $failures -eq 0 ]]; then
        printf '%s\n' "${GREEN}${BOLD}All checks passed!${NC}"
    else
        printf '%s\n' "${RED}${BOLD}${failures} check(s) failed.${NC}"
    fi
    return $failures
}

cmd_test() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"

    local env_vars="AUTHD_SKIP_ROOT_TESTS=1 "
    local go_args=()
    for arg in "$@"; do
        if [[ "$arg" == "--update-golden" ]]; then
            env_vars+="TESTS_UPDATE_GOLDEN=1 "
        elif [[ "$arg" == "--skip-external" ]]; then
            env_vars+="AUTHD_SKIP_EXTERNAL_DEPENDENT_TESTS=1 "
        else
            go_args+=("$arg")
        fi
    done

    # Default to running all tests with race detection per AGENTS.md
    [[ ${#go_args[@]} -eq 0 ]] && go_args=("-race" "./...")

    info "Running tests: ${env_vars}go test ${go_args[*]}"
    exec_in_vm "cd ${WORKSPACE_PATH} && env ${env_vars} go test ${go_args[*]}"
}

cmd_build() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"

    local component="${1:-all}"
    local ws="${WORKSPACE_PATH}"

    case "$component" in
        authd)
            info "Rebuilding authd daemon..."
            exec_in_vm "cd ${ws} && ./dev/scripts/install-authd --daemon-only"
            ok "authd rebuilt and restarted"
            ;;
        pam)
            info "Rebuilding PAM modules..."
            exec_in_vm "cd ${ws} && ./dev/scripts/install-authd --pam-only"
            ok "PAM rebuilt (reconnect SSH sessions to load new module)"
            ;;
        nss)
            info "Rebuilding NSS module..."
            exec_in_vm "cd ${ws} && ./dev/scripts/install-authd --nss-only"
            ok "NSS rebuilt and ldconfig refreshed"
            ;;
        broker)
            local variant="${2:-}"
            [[ -n "$variant" ]] || die "Usage: ./dev/dev-env.sh build broker <variant>"
            local tag="" binary="authd-${variant}"
            case "$variant" in
                google)    tag="-tags=withgoogle" ;;
                msentraid) tag="-tags=withmsentraid" ;;
                oidc)      tag="" ;;
                *)         die "Unknown variant: $variant. Use: google, msentraid, or oidc" ;;
            esac
            info "Rebuilding ${variant} broker..."
            exec_in_vm "cd ${ws}/authd-oidc-brokers && go build ${tag} -o /tmp/${binary} ./cmd/authd-oidc && sudo install -m 755 /tmp/${binary} /usr/libexec/${binary} && sudo systemctl restart ${binary}"
            ok "Broker ${variant} rebuilt and restarted"
            ;;
        all)
            info "Rebuilding everything via install-authd..."
            exec_in_vm "cd ${ws} && ./dev/scripts/install-authd"
            ;;
        *)
            cat <<EOF
${BOLD}Usage:${NC} ./dev/dev-env.sh build [component]

${BOLD}Components:${NC}
  authd             Rebuild authd daemon + regen proto + restart
  pam               Rebuild PAM modules (reconnect SSH to load)
  nss               Rebuild NSS module + ldconfig
  broker <variant>  Rebuild broker binary + restart (google/msentraid/oidc)
                    Note: use 'broker <variant>' to update credentials instead
  all               Run full install-authd (default)
EOF
            ;;
    esac
}

cmd_logs() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"
    local target="${1:-all}"

    if [[ "$target" == "all" ]]; then
        # Combine logs for authd daemon + all broker variants + PAM module,
        # mirroring the maintainer's journal script from adombeck/authd-scripts.
        lxc exec "$CONTAINER_NAME" -- journalctl -f \
            _SYSTEMD_UNIT=authd.service + \
            UNIT=authd.service + \
            _SYSTEMD_UNIT=authd-google.service + \
            UNIT=authd-google.service + \
            _SYSTEMD_UNIT=authd-msentraid.service + \
            UNIT=authd-msentraid.service + \
            _SYSTEMD_UNIT=authd-oidc.service + \
            UNIT=authd-oidc.service + \
            _COMM=authd-pam
        return
    fi

    local unit
    case "$target" in
        authd)       unit="authd" ;;
        cloud-init)  lxc exec "$CONTAINER_NAME" -- tail -f /var/log/cloud-init-output.log; return ;;
        google|msentraid|oidc) unit="authd-${target}" ;;
        *)           unit="$target" ;;
    esac
    lxc exec "$CONTAINER_NAME" -- journalctl -fu "$unit"
}

cmd_exec() {
    container_running || die "VM '${CONTAINER_NAME}' is not running. Run: ./dev/dev-env.sh up"
    [[ $# -gt 0 ]] || die "Usage: ./dev/dev-env.sh exec <command>"
    local cmd
    cmd=$(printf ' %q' "$@")
    lxc exec "$CONTAINER_NAME" -- su -l ubuntu -c "cd ${WORKSPACE_PATH} &&${cmd}"
}

cmd_help() {
    cat <<EOF
${BOLD}authd Development Environment Manager${NC}

Creates an LXD VM with all build/test dependencies for authd.
The host source tree is bind-mounted into the VM at ${WORKSPACE_PATH}.
The VM runs full systemd + D-Bus + SSH + GDM for integration testing.

${BOLD}Usage:${NC} ./dev/dev-env.sh [global-flags] <command> [command-options]

${BOLD}Global flags${NC} (must come before the subcommand, apply to all commands):
  --name NAME         VM name (default: authd-dev)
  --release NAME      Ubuntu release image (e.g. noble, jammy, 24.04, 22.04) (default: noble)
  --workspace PATH    Workspace path inside the VM (default: /workspace/authd)

${BOLD}Commands:${NC}
  up                  Create, provision, and build authd in the dev VM
  stop                Stop the VM (preserves state; restart with 'up')
  down [--force]      Stop and delete the VM
  shell               Open a shell via lxc exec (no SSH needed)
  ssh                 Connect via SSH
  status [--deep]     Show VM status and snapshots (use --deep for validation)
  snapshot <name>     Create a named snapshot
  restore <name>      Restore a named snapshot
  broker <variant>    Configure/install a broker; use 'edit' subaction to open broker.conf
  build [comp]        Fast rebuild a component (authd/pam/nss/broker/all)
  validate            Check authd stack health (socket, PAM, NSS, brokers)
  test [--update-golden] [--skip-external] ...
  logs [target|all]   Tail logs (authd/google/msentraid/oidc/cloud-init/all)
  exec <cmd>          Run a command inside the VM (in workspace dir)
  ip                  Print the VM's current IP address
  help                Show this help

${BOLD}Examples:${NC}
  ./dev/dev-env.sh up                                   # Create with defaults (noble)
  ./dev/dev-env.sh --release jammy up                   # Use Ubuntu 22.04 instead
  ./dev/dev-env.sh down --force && ./dev/dev-env.sh up  # Nuke and rebuild from scratch
  ./dev/dev-env.sh build authd                          # Rebuild authd fast
  ./dev/dev-env.sh status --deep                        # Check if internal components are healthy
  ./dev/dev-env.sh logs                                 # Tail all authd logs

${BOLD}Typical workflow:${NC}
  1. ./dev/dev-env.sh up                                        # Create VM + build authd
  2. ./dev/dev-env.sh broker google \\                          # Configure broker (or msentraid/oidc)
         --client-id YOUR_ID --client-secret YOUR_SECRET \\
         --ssh-suffixes '@gmail.com'
  3. ssh user@gmail.com@\$(./dev/dev-env.sh ip)                 # Test PAM login from host
  4. ./dev/dev-env.sh test                                      # Run all tests with race detection
  5. ./dev/dev-env.sh build authd                               # Rebuild after code changes
  6. ./dev/dev-env.sh logs authd                                # Debug with logs
  7. ./dev/dev-env.sh restore installed                         # Reset to freshly built state

EOF
}

# --- Main ---

COMMAND=""
CMD_ARGS=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)      [[ $# -ge 2 ]] || die "--name requires a value";      CONTAINER_NAME="$2"; PROFILE_NAME="$2"; shift 2 ;;
        --release)   [[ $# -ge 2 ]] || die "--release requires a value";   RELEASE="$2"; shift 2 ;;
        --workspace) [[ $# -ge 2 ]] || die "--workspace requires a value"; WORKSPACE_PATH="$2"; shift 2 ;;
        help|--help|-h) COMMAND="help"; shift ;;
        -*)          CMD_ARGS+=("$1"); shift ;;
        *)
            if [[ -z "$COMMAND" ]]; then
                COMMAND="$1"
            else
                CMD_ARGS+=("$1")
            fi
            shift
            ;;
    esac
done

COMMAND="${COMMAND:-help}"

case "$COMMAND" in
    up)         cmd_up "${CMD_ARGS[@]}" ;;
    down)       cmd_down "${CMD_ARGS[@]}" ;;
    stop)       cmd_stop ;;
    shell)      cmd_shell ;;
    ssh)        cmd_ssh ;;
    status)     cmd_status "${CMD_ARGS[@]}" ;;
    snapshot)   cmd_snapshot "${CMD_ARGS[@]}" ;;
    restore)    cmd_restore "${CMD_ARGS[@]}" ;;
    broker)     cmd_broker "${CMD_ARGS[@]}" ;;
    build)      cmd_build "${CMD_ARGS[@]}" ;;
    validate)   cmd_validate ;;
    test)       cmd_test "${CMD_ARGS[@]}" ;;
    logs)       cmd_logs "${CMD_ARGS[@]}" ;;
    exec)       cmd_exec "${CMD_ARGS[@]}" ;;
    ip)         cmd_ip ;;
    help)       cmd_help ;;
    *)          die "Unknown command: $COMMAND — run './dev/dev-env.sh help' for usage." ;;
esac
