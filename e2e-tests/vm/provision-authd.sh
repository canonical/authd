#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
LIB_DIR="${SCRIPT_DIR}/lib"
SSH="${SCRIPT_DIR}/ssh.sh"
SCP="${SCRIPT_DIR}/scp.sh"
DATA_DIR_ARG=

usage(){
    cat << EOF
Usage: $0 [--config-file <file>] [--release <release>] [--data-dir <directory>] [--authd-deb <deb>] [--apt-source <source>] [--authd-apt-source <source>] [--apt-source-base <source>] [--authd-apt-source-base <source>] [--broker-snap <snap>]

Options:
   --config-file <file>  Path to the configuration file (default: config.env)
   --release <release>   Ubuntu release to provision (e.g. noble, resolute); overrides config file
   --data-dir <directory>
                        Base directory for VM artifacts (or AUTHD_E2E_DATA_DIR)
   --force              Force installation of authd and brokers even if snapshots already exist.
                        The existing snapshots will be deleted and recreated with the new installation.
   --broker <broker>    The broker to install ("authd-google", "authd-msentraid", ...)
   --authd-deb <deb>    Path to the authd deb file to install
   --apt-source <source>
                        PPA or Ubuntu archive suite for all packages except
                        authd
   --authd-apt-source <source>
                        PPA or Ubuntu archive suite from which to install
                        authd
   --apt-source-base <source>
                        Ubuntu archive suite from which to install the stable
                        system package baseline for migration tests
   --authd-apt-source-base <source>
                        PPA or Ubuntu archive suite from which to install the
                        stable authd baseline for migration tests
   --broker-snap <snap> Path to the broker snap file to install (default: install from the edge channel)
  -h, --help             Show this help message and exit

Provisions authd in the VM for end-to-end tests
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --config-file)
            CONFIG_FILE="$2"
            shift 2
            ;;
        --release)
            RELEASE_ARG="$2"
            shift 2
            ;;
        --data-dir)
            DATA_DIR_ARG="$2"
            shift 2
            ;;
        --force)
            FORCE=true
            shift
            ;;
        --broker)
            BROKER="$2"
            shift 2
            ;;
        --authd-deb)
            AUTHD_DEB="$2"
            shift 2
            ;;
        --apt-source-base)
            APT_SOURCE_BASE_ARG="$2"
            shift 2
            ;;
        --authd-apt-source-base)
            AUTHD_APT_SOURCE_BASE_ARG="$2"
            shift 2
            ;;
        --apt-source)
            APT_SOURCE_ARG="$2"
            shift 2
            ;;
        --authd-apt-source)
            AUTHD_APT_SOURCE_ARG="$2"
            shift 2
            ;;
        --broker-snap)
            BROKER_SNAP="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        -*)
            echo >&2 "Unknown option: $1"
            exit 1
            ;;
        *)
            echo >&2 "Unexpected positional argument: $1"
            exit 1
    esac
done

# Validate --authd-deb if provided
if [ -n "${AUTHD_DEB:-}" ] && [ ! -f "${AUTHD_DEB}" ]; then
    echo "authd deb file '${AUTHD_DEB}' not found." >&2
    exit 1
fi

# Validate --broker-snap if provided
if [ -n "${BROKER_SNAP:-}" ] && [ ! -f "${BROKER_SNAP}" ]; then
    echo "Broker snap file '${BROKER_SNAP}' not found." >&2
    exit 1
fi

# Validate config file if provided
if [ -n "${CONFIG_FILE:-}" ] && [ ! -f "${CONFIG_FILE}" ]; then
    echo "Configuration file '${CONFIG_FILE}' not found." >&2
    exit 1
fi

# Set default config file if not provided
if [ -z "${CONFIG_FILE:-}" ]; then
    CONFIG_FILE="${SCRIPT_DIR}/config.env"
    if [[ ! -f "${CONFIG_FILE}" ]]; then
        # Fall back to the main worktree when running from a linked worktree.
        # git rev-parse --git-common-dir returns an absolute path only for
        # linked worktrees; in the main worktree it returns a relative ".git".
        _git_common_dir=$(git -C "${SCRIPT_DIR}" rev-parse --git-common-dir 2>/dev/null || true)
        if [[ "${_git_common_dir}" == /* ]]; then
            CONFIG_FILE="$(dirname "${_git_common_dir}")/e2e-tests/vm/config.env"
        fi
        unset _git_common_dir
    fi
fi

# Load the configuration file (if it exists)
if [ -f "${CONFIG_FILE}" ]; then
    set -a
    # shellcheck source=config.env disable=SC1091
    source "${CONFIG_FILE}"
    set +a
fi

# shellcheck source=lib/libprovision.sh
source "${LIB_DIR}/libprovision.sh"

# Load broker-specific credentials from e2e-tests-<broker>.env if it exists.
# BROKER is known at this point (from CLI args or config file).
if [ -n "${BROKER:-}" ]; then
    _env_file="${SCRIPT_DIR}/../e2e-tests-${BROKER#authd-}.env"
    if [[ ! -f "${_env_file}" ]]; then
        # Fall back to the main worktree when running from a linked worktree.
        # git rev-parse --git-common-dir returns an absolute path only for
        # linked worktrees; in the main worktree it returns a relative ".git".
        _git_common_dir=$(git -C "${SCRIPT_DIR}" rev-parse --git-common-dir 2>/dev/null || true)
        if [[ "${_git_common_dir}" == /* ]]; then
            _env_file="$(dirname "${_git_common_dir}")/e2e-tests/e2e-tests-${BROKER#authd-}.env"
        fi
    fi
    if [ -f "${_env_file}" ]; then
        set -a
        # shellcheck disable=SC1090
        source "${_env_file}"
        set +a
    fi
    unset _env_file _git_common_dir
fi

# CLI options override config file values
RELEASE="${RELEASE_ARG:-${RELEASE:-}}"
DATA_DIR="${DATA_DIR_ARG:-${AUTHD_E2E_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/authd-e2e-tests}}"
if [[ -n "${DATA_DIR_ARG}" || -n "${AUTHD_E2E_DATA_DIR:-}" ]]; then
    ARTIFACTS_DIR="${DATA_DIR}/${RELEASE}"
fi
requested_apt_source_base="${APT_SOURCE_BASE_ARG:-${APT_SOURCE_BASE:-}}"
requested_authd_apt_source_base="${AUTHD_APT_SOURCE_BASE_ARG:-${AUTHD_APT_SOURCE_BASE:-}}"
requested_apt_source="${APT_SOURCE_ARG:-${APT_SOURCE:-${AUTHD_DEFAULT_APT_SOURCE}}}"
requested_authd_apt_source="${AUTHD_APT_SOURCE_ARG:-${AUTHD_APT_SOURCE:-}}"

APT_SOURCE_BASE=
if [ -n "${requested_apt_source_base}" ]; then
    if ! APT_SOURCE_BASE="$(normalize_apt_source "${requested_apt_source_base}")"; then
        echo "Invalid base APT source '${requested_apt_source_base}'." >&2
        exit 1
    fi
    if is_ppa_source "${APT_SOURCE_BASE}"; then
        echo "Base APT source must be an Ubuntu archive suite." >&2
        exit 1
    fi
fi

AUTHD_APT_SOURCE_BASE=
if [ -n "${requested_authd_apt_source_base}" ]; then
    if ! AUTHD_APT_SOURCE_BASE="$(normalize_apt_source "${requested_authd_apt_source_base}")"; then
        echo "Invalid authd base APT source '${requested_authd_apt_source_base}'." >&2
        exit 1
    fi
fi

if ! APT_SOURCE="$(normalize_apt_source "${requested_apt_source}")"; then
    echo "Invalid APT source '${requested_apt_source}'." >&2
    exit 1
fi

AUTHD_APT_SOURCE=
if [ -n "${requested_authd_apt_source}" ]; then
    if ! AUTHD_APT_SOURCE="$(normalize_apt_source "${requested_authd_apt_source}")"; then
        echo "Invalid authd APT source '${requested_authd_apt_source}'." >&2
        exit 1
    fi
    if [ -n "${AUTHD_DEB:-}" ]; then
        echo "--authd-apt-source cannot be used together with --authd-deb." >&2
        exit 1
    fi
fi
unset requested_apt_source_base requested_authd_apt_source_base requested_apt_source requested_authd_apt_source

VM_NAME_BASE="${VM_NAME_BASE:-e2e-runner}"

assert_env_vars RELEASE BROKER

VM_RELEASE=$(resolve_devel_release "${RELEASE}")

validate_archive_source() {
    local source="$1"
    local source_name="$2"

    if [ -z "${source}" ] || is_ppa_source "${source}"; then
        return
    fi

    if [[ "${source}" != "${VM_RELEASE}" && "${source}" != "${VM_RELEASE}-"* ]]; then
        echo "${source_name} APT source '${source}' does not match VM release '${VM_RELEASE}'." >&2
        exit 1
    fi
}

validate_archive_source "${APT_SOURCE_BASE:-}" base
validate_archive_source "${AUTHD_APT_SOURCE_BASE:-}" authd-base
validate_archive_source "${APT_SOURCE}" target
validate_archive_source "${AUTHD_APT_SOURCE:-}" authd

ARTIFACTS_DIR="${ARTIFACTS_DIR:-${DATA_DIR}/${RELEASE}}"

if [ -z "${VM_NAME:-}" ]; then
    export VM_NAME="${VM_NAME_BASE}-${RELEASE}"
fi
export VM_NAME

# Check if we have all required artifacts
IMAGE="${ARTIFACTS_DIR}/${VM_NAME}.qcow2"
if [ ! -f "${IMAGE}" ]; then
    echo "Image not found: ${IMAGE}. Please run e2e-tests/vm/provision-ubuntu.sh first."
    exit 1
fi

LIBVIRT_XML="${ARTIFACTS_DIR}/${VM_NAME_BASE}.xml"
if [ ! -f "${LIBVIRT_XML}" ]; then
    echo "Libvirt XML file not found: ${LIBVIRT_XML}. Please run e2e-tests/vm/provision-ubuntu.sh first."
    exit 1
fi

INITIAL_SETUP_SNAPSHOT="initial-setup"
AUTHD_STABLE_SNAPSHOT="authd-stable-installed"
BROKER_STABLE_SNAPSHOT="${BROKER}-stable-installed"
AUTHD_SNAPSHOT="authd-installed"
BROKER_SNAPSHOT="${BROKER}-installed"
if [ -n "${AUTHD_APT_SOURCE_BASE:-}" ]; then
    STABLE_AUTHD_SOURCE="${AUTHD_APT_SOURCE_BASE}"
else
    stable_authd_ppa="ubuntu-enterprise-desktop/authd"
    if ppa_has_suite "${stable_authd_ppa}" "${VM_RELEASE}"; then
        STABLE_AUTHD_SOURCE="ppa:${stable_authd_ppa}"
    else
        ppa_check_status=$?
        if [ "${ppa_check_status}" -ne 1 ]; then
            echo "Could not verify whether the stable authd PPA publishes '${VM_RELEASE}'." >&2
            exit 1
        fi
        STABLE_AUTHD_SOURCE="${VM_RELEASE}"
        echo "The stable authd PPA does not publish '${VM_RELEASE}'; using the Ubuntu archive for the baseline."
    fi
fi
unset ppa_check_status stable_authd_ppa
# Stable snapshot names are used by the test suite, so track their source pair
# separately and rebuild both snapshots when either source changes.
STABLE_SNAPSHOT_SOURCE_KEY="${APT_SOURCE_BASE:-<none>}|${STABLE_AUTHD_SOURCE}"
STABLE_SNAPSHOT_SOURCE_FILE="${ARTIFACTS_DIR}/${VM_NAME}.${BROKER}.stable-snapshot-source"

function install_broker() {
    local broker="$1"
    local channel
    local snap_file

    case "$2" in
        --channel)
            channel="$3"
            ;;
        --snap)
            snap_file="$3"
            ;;
    esac

    local broker_config="${broker#authd-}.conf"

    # Get the issuer ID from the environment variable corresponding to the broker.
    # For example, for broker "authd-msentraid", we use "AUTHD_MSENTRAID_ISSUER_ID".
    local broker_prefix="${broker^^}"
    broker_prefix="${broker_prefix//-/_}"
    local issuer_id_var="${broker_prefix}_ISSUER_ID"
    local client_id_var="${broker_prefix}_CLIENT_ID"
    local client_secret_var="${broker_prefix}_CLIENT_SECRET"

    # Assert that required environment variables are set.
    # The issuer ID is optional (authd-google has a default one).
    # The client secret is also optional (authd-msentraid does not require it).
    local broker_env_template="e2e-tests/e2e-tests-${broker#authd-}.env.template"
    assert_env_vars --template "${broker_env_template}" "${client_id_var}"

    local issuer_id="${!issuer_id_var:-}"
    local client_id="${!client_id_var}"
    local client_secret="${!client_secret_var:-}"

    if [ -n "${snap_file:-}" ]; then
        # Copy the local snap to the VM and install it
        local remote_snap
        remote_snap="/home/ubuntu/$(basename "${snap_file}")"
        "${SCP}" "${snap_file}" "${remote_snap}"
        $SSH snap install --dangerous "${remote_snap}"
    else
        # Install the snap from the specified channel
        $SSH snap install "${broker}" --channel="${channel}"
    fi

    # Configure broker and restart services
    $SSH bash -euo pipefail -s <<-EOF
		cp /snap/${broker}/current/conf/authd/${broker_config} /etc/authd/brokers.d/
		sed -i \
			-e "s|<ISSUER_ID>|${issuer_id}|g" \
			-e "s|<CLIENT_ID>|${client_id}|g" \
			-e "s|<CLIENT_SECRET>|${client_secret}|g" \
			-e "s/^#*device_code = .*/device_code = true/" \
			-e "s/^#*entra_auth = .*/entra_auth = false/" \
			/var/snap/${broker}/current/broker.conf
		echo 'verbosity: 2' > /var/snap/${broker}/current/${broker}.yaml
		systemctl restart authd.service
		snap restart "${broker}"
	EOF

    # A guest reboot can leave the domain unavailable while libvirt is still
    # waiting for the guest to transition. Shut it down and start it
    # explicitly so we wait for both state changes.
    reboot_system
}

function add_apt_source() {
    local apt_source="$1"

    if is_ppa_source "${apt_source}"; then
        local ppa="${apt_source#ppa:}"
        local cmd="add-apt-repository -y -n ppa:${ppa}"
        # Launchpad is sometimes slow to respond, so retry PPA additions.
        retry --times 5 --delay 3 -- "$SSH" -- "$cmd"
    elif [[ "${apt_source}" == "${VM_RELEASE:-}" ]]; then
        # The base suite is already enabled.
        return
    else
        $SSH "add-apt-repository -y -n -S 'deb http://archive.ubuntu.com/ubuntu/ ${apt_source} main restricted universe multiverse'"
    fi
}

function configure_apt_policy() {
    local system_source="$1"
    local authd_source="$2"
    local system_pin
    local authd_pin
    local authd_fallback_policy=

    system_pin="$(source_pin "${system_source}")"
    authd_pin="$(source_pin "${authd_source}")"
    if [ "${system_source}" != "${authd_source}" ]; then
        authd_fallback_policy=$(
            cat <<-PREFERENCE
			Package: *
			Pin: release ${authd_pin}
			Pin-Priority: 100
			PREFERENCE
        )
    fi

    $SSH bash -euo pipefail -s <<-EOF
		mkdir -p /etc/apt/preferences.d
		cat > /etc/apt/preferences.d/99-e2e-system-source <<-PREFERENCE
		Package: *
		Pin: release ${system_pin}
		Pin-Priority: 990
		PREFERENCE
		cat > /etc/apt/preferences.d/99-e2e-authd-source <<-PREFERENCE
		Package: authd
		Pin: release ${authd_pin}
		Pin-Priority: 1001
		PREFERENCE
		if [ -n '${authd_fallback_policy}' ]; then
			cat > /etc/apt/preferences.d/98-e2e-authd-source-fallback <<-PREFERENCE
${authd_fallback_policy}
			PREFERENCE
		else
			rm -f /etc/apt/preferences.d/98-e2e-authd-source-fallback
		fi
	EOF
}

function configure_authd_only_policy() {
    local authd_source="$1"
    local authd_pin

    authd_pin="$(source_pin "${authd_source}")"
    $SSH bash -euo pipefail -s <<-EOF
		mkdir -p /etc/apt/preferences.d
		cat > /etc/apt/preferences.d/99-e2e-authd-source <<-PREFERENCE
		Package: authd
		Pin: release ${authd_pin}
		Pin-Priority: 1001
		PREFERENCE
	EOF
}

function configure_local_authd_policy() {
    $SSH bash -euo pipefail -s <<-'EOF'
		authd_version="$(dpkg-query -W -f='${Version}' authd)"
		cat > /etc/apt/preferences.d/99-e2e-authd-source <<-PREFERENCE
		Package: authd
		Pin: version ${authd_version}
		Pin-Priority: 1001
		PREFERENCE
		apt-mark hold authd
	EOF
}

function verify_authd_source() {
    local expected_source="$1"
    local expected_reference

    expected_reference="$(source_policy_reference "${expected_source}")"

    $SSH bash -euo pipefail -s <<-EOF
		apt_policy="\$(apt-cache policy authd)"
		installed_version="\$(dpkg-query -W -f='\${Version}' authd)"
		candidate_version="\$(awk '/Candidate:/ { print \$2; exit }' <<<"\${apt_policy}")"
		if [ -z "\${installed_version}" ] || [ "\${candidate_version}" != "\${installed_version}" ]; then
			echo "authd candidate \${candidate_version} does not match installed version \${installed_version}" >&2
			printf '%s\n' "\${apt_policy}" >&2
			exit 1
		fi
		if ! grep -Fq '${expected_reference}' <<<"\${apt_policy}"; then
			echo "authd APT policy does not contain the selected source '${expected_source}'" >&2
			printf '%s\n' "\${apt_policy}" >&2
			exit 1
		fi
	EOF
}

function verify_local_authd() {
    $SSH bash -euo pipefail -s <<-'EOF'
		installed_version="$(dpkg-query -W -f='${Version}' authd)"
		if [ -z "${installed_version}" ] || ! apt-mark showhold | grep -qx authd; then
			echo "authd is not held at the locally supplied package version" >&2
			apt-cache policy authd >&2
			exit 1
		fi
	EOF
}

# Print executed commands to ease debugging
set -x

# Define the VM
if ! virsh dominfo "${VM_NAME}" &> /dev/null; then
    virsh define "${LIBVIRT_XML}"
fi

if has_snapshot "$INITIAL_SETUP_SNAPSHOT"; then
    PRE_AUTHD_SNAPSHOT="${INITIAL_SETUP_SNAPSHOT}"
else
    PRE_AUTHD_SNAPSHOT="pre-authd-setup"
fi

if has_snapshot "$PRE_AUTHD_SNAPSHOT"; then
    restore_snapshot_and_sync_time "$PRE_AUTHD_SNAPSHOT"
else
    # Ensure the VM is running to perform initial setup
    boot_system
    # Create a pre-authd setup snapshot
    force_create_snapshot "$PRE_AUTHD_SNAPSHOT"
fi

if [ -n "${FORCE:-}" ] ||
    [ ! -f "${STABLE_SNAPSHOT_SOURCE_FILE}" ] ||
    [ "$(cat "${STABLE_SNAPSHOT_SOURCE_FILE}")" != "${STABLE_SNAPSHOT_SOURCE_KEY}" ] ||
    ! has_snapshot "${AUTHD_STABLE_SNAPSHOT}" ||
    ! has_snapshot "${BROKER_STABLE_SNAPSHOT}"; then
    REBUILD_STABLE_SNAPSHOTS=true
fi

if [ -z "${REBUILD_STABLE_SNAPSHOTS:-}" ] && has_snapshot "${AUTHD_STABLE_SNAPSHOT}"; then
    restore_snapshot_and_sync_time "${AUTHD_STABLE_SNAPSHOT}"
else
    # Install authd stable and create the migration baseline. When a system
    # source is selected, pin all packages to it and authd to its own source.
    if [ -n "${APT_SOURCE_BASE:-}" ] || [ -n "${AUTHD_APT_SOURCE_BASE:-}" ]; then
        if [ -n "${APT_SOURCE_BASE:-}" ]; then
            add_apt_source "${APT_SOURCE_BASE}"
        fi
        if [ "${STABLE_AUTHD_SOURCE}" != "${APT_SOURCE_BASE:-}" ]; then
            add_apt_source "${STABLE_AUTHD_SOURCE}"
        fi
        $SSH apt-get update
        if [ -n "${APT_SOURCE_BASE:-}" ]; then
            configure_apt_policy "${APT_SOURCE_BASE}" "${STABLE_AUTHD_SOURCE}"
            $SSH apt-get install -y authd
            $SSH apt-get full-upgrade -y
        else
            configure_authd_only_policy "${STABLE_AUTHD_SOURCE}"
            $SSH apt-get install -y authd
        fi
    else
        add_apt_source "${STABLE_AUTHD_SOURCE}"
        $SSH apt-get update
        $SSH "apt-get install -y authd"
    fi
    force_create_snapshot "${AUTHD_STABLE_SNAPSHOT}"
fi

if [ -z "${REBUILD_STABLE_SNAPSHOTS:-}" ] && has_snapshot "${BROKER_STABLE_SNAPSHOT}"; then
    restore_snapshot_and_sync_time "${BROKER_STABLE_SNAPSHOT}"
else
    install_broker "${BROKER}" --channel "stable"
    # Snapshot this broker installation
    force_create_snapshot "${BROKER_STABLE_SNAPSHOT}"
fi

printf '%s\n' "${STABLE_SNAPSHOT_SOURCE_KEY}" > "${STABLE_SNAPSHOT_SOURCE_FILE}"
unset REBUILD_STABLE_SNAPSHOTS

# Remove the authd-stable-installed snapshot which is no longer needed
# virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "authd-stable-installed"

# Revert to the pre-authd setup snapshot before installing the version to test
restore_snapshot_and_sync_time "$PRE_AUTHD_SNAPSHOT"

# Add the selected sources. The system source is also the default authd source
# when no local package or explicit authd source was requested.
add_apt_source "${APT_SOURCE}"
if [ -n "${AUTHD_APT_SOURCE:-}" ] && [ "${AUTHD_APT_SOURCE}" != "${APT_SOURCE}" ]; then
    add_apt_source "${AUTHD_APT_SOURCE}"
fi

# Pin the system source for all packages, then override authd with its
# independently selected source. This keeps authd dependencies on the normal
# system policy and avoids apt-get -t changing dependency selection globally.
AUTHD_POLICY_SOURCE="${AUTHD_APT_SOURCE:-${APT_SOURCE}}"
configure_apt_policy "${APT_SOURCE}" "${AUTHD_POLICY_SOURCE}"

# Configure authd to be verbose. We do this before installing authd to avoid
# having to restart the service after installation (just a simple optimization).
$SSH bash -euo pipefail -s <<-EOF
    mkdir -p /etc/systemd/system/authd.service.d
    cat <<-UNIT > /etc/systemd/system/authd.service.d/override.conf
		[Service]
		ExecStart=
		ExecStart=/usr/libexec/authd -vv
	UNIT
EOF

# Refresh metadata and update the whole system from the selected system source.
$SSH apt-get update
# authd constrains gnome-shell with a compatibility rule. Explicitly
# install it from the selected source because the base image's archive version
# may not satisfy authd's rule.
$SSH apt-get install -y gnome-shell
if [ -n "${AUTHD_DEB:-}" ]; then
    # Keep a stable authd package out of the system upgrade until the local
    # package is installed. The package is absent in the normal target image,
    # so this is only needed by callers that start from an authd snapshot.
    $SSH bash -euo pipefail -s <<-'EOF'
		if dpkg-query -W -f='${db:Status-Abbrev}' authd 2>/dev/null | grep -q '^ii '; then
			apt-mark hold authd
		fi
	EOF
fi
$SSH apt-get full-upgrade -y

if [ -n "${AUTHD_DEB:-}" ]; then
    "${SCP}" "${AUTHD_DEB}" "/home/ubuntu/$(basename "${AUTHD_DEB}")"
    $SSH bash -euo pipefail -s <<-EOF
		apt-mark unhold authd 2>/dev/null || true
		apt-get install -y --allow-downgrades "/home/ubuntu/$(basename "${AUTHD_DEB}")"
	EOF
    configure_local_authd_policy
    verify_local_authd
else
    $SSH apt-get install -y --allow-downgrades authd
    verify_authd_source "${AUTHD_POLICY_SOURCE}"
fi

# Configure the PAM module to be verbose as well
$SSH bash -euo pipefail -s <<-EOF
    sed -i '/pam_authd_exec\.so\|pam_authd\.so/ s/$/ debug=true/' /etc/pam.d/*
EOF

force_create_snapshot "${AUTHD_SNAPSHOT}"

# Install the brokers for the version of authd to test.
if [ -n "${BROKER_SNAP:-}" ]; then
    install_broker "${BROKER}" --snap "${BROKER_SNAP}"
else
    install_broker "${BROKER}" --channel "edge"
fi

force_create_snapshot "${BROKER_SNAPSHOT}"

# Remove the authd-installed snapshot which is no longer needed
virsh snapshot-delete --domain "${VM_NAME}" --snapshotname "${AUTHD_SNAPSHOT}"
