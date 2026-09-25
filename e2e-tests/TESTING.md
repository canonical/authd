# End-to-end tests

The end-to-end tests are implemented using [YARF][yarf].
They cover a wide range of scenarios, both for authd and the brokers.

## Setting up the environment

The E2E scripts use these optional path variables:

- `AUTHD_E2E_DATA_DIR`: base directory for VM images, snapshots, and related
  artifacts. It defaults to `${XDG_DATA_HOME:-$HOME/.local/share}/authd-e2e-tests`.
- `AUTHD_E2E_TEST_RUNS_DIR`: directory for test and YARF console output. It
  defaults to `${XDG_RUNTIME_DIR:-/tmp}/authd-e2e-test-runs`.

These variables are useful when the test process runs in a sandbox. The same
paths can be supplied explicitly with `vm/provision.sh --data-dir` and
`run-tests.sh` or `yarf-console.sh --test-runs-dir`.

### 1. Install dependencies

```bash
# Dependencies for provisioning the VM
sudo ./e2e-tests/vm/install-provision-deps.sh

# Dependencies for running the tests
sudo ./e2e-tests/install-deps.sh
```

### 2. Configure

#### Broker credentials

For each broker you want to test, copy the corresponding template and fill in
your credentials (these files are gitignored):

- For `authd-google`:
  ```bash
  cp e2e-tests/e2e-tests-google.env.template e2e-tests/e2e-tests-google.env
  ```
- For `authd-msentraid`:
  ```bash
  cp e2e-tests/e2e-tests-msentraid.env.template e2e-tests/e2e-tests-msentraid.env
  ```

#### VM provisioning config

Copy `e2e-tests/vm/config.env.template` to `e2e-tests/vm/config.env` and set
your SSH public key path (and optionally the default Ubuntu release and VM name
prefix).

If your SSH key is protected by a passphrase, add it to ssh-agent before
provisioning. Note that ssh-agent entries do not persist across sessions, so
you will need to run this again each time you start a new session:

```bash
ssh-add /path/to/key
```

### 3. Provision the VM

```bash
./e2e-tests/vm/provision.sh --broker <broker> --release <release>
```

This sets up a libvirt VM with Ubuntu, installs authd and the broker, and
creates the snapshots required by the tests. By default, packages other than
authd come from the [authd-edge PPA][authd-edge-ppa], authd comes from the local
package when `--authd-deb` is supplied, and the broker comes from the edge
channel snap. Use `--authd-apt-source <source>` to select the source for authd
or `--apt-source <source>` to select the source for all packages except authd.
Each source can be `authd`, `authd-edge`, `authd-dev`, or an Ubuntu archive
suite. Use `--authd-deb` for a local authd package; it cannot be combined with
`--authd-apt-source`. Use `--broker-snap` to install a locally built broker
snap. Run `./e2e-tests/vm/provision.sh --help` for all available options,
including `--force` to reprovision.

APT policy pins authd to its selected source while allowing authd dependencies
to use the system source and normal archive fallbacks. Provisioning upgrades
the whole system from the system source, not only authd dependencies.

To test migration from one source set to another, pass the stable system
baseline with `--apt-source-base`, the stable authd baseline with
`--authd-apt-source-base`, the system package target with `--apt-source`, and
the authd target with `--authd-apt-source`:

```bash
./e2e-tests/vm/provision.sh \
  --release resolute \
  --broker authd-google \
  --apt-source-base resolute-updates \
  --authd-apt-source-base authd \
  --apt-source resolute-proposed \
  --authd-apt-source authd-edge \
  --force
```

The base sources install the stable snapshot. The target sources install the
version under test and update the system packages. Provisioning records the
normalized base-source pair and rebuilds both stable snapshots when either
source changes. Use `--force` to rebuild them regardless.

### 4. Set up YARF

```bash
./e2e-tests/setup-yarf.sh
```

This initializes the YARF git submodule and installs it into a Python virtual
environment.

## Running the tests

```bash
./e2e-tests/run-tests.sh --broker <broker> --release <release> [test.robot...]
```

`run-tests.sh` automatically loads the broker's `.env` file (e.g.
`e2e-tests-google.env` for `authd-google`). Omit the test file argument to run
the full suite. To run one test case from a suite, pass its exact name with
`--test`:

```bash
./e2e-tests/run-tests.sh \
    --broker authd-google --release noble \
    --test "Test second login succeeds with force_access_check_with_provider enabled" \
    e2e-tests/tests/force_access_check_with_provider.robot
```

Run `./e2e-tests/run-tests.sh --help` for all available options, including
`--rerunfailed`, `--output-dir`, and `--test-runs-dir`.

After a successful run, the e2e VM is stopped. If a test fails, the VM remains
running for investigation.

### Reusing a prepared VM snapshot

For local development, it can be useful to run an e2e test against a
modified VM state, for example to quickly test changes to the authd
installation. To do that, create a named snapshot after making the desired
modifications in the VM. Create it while the VM is running so the snapshot
includes its memory state. You can use the snapshot helper for that:

```bash
RELEASE=noble
VM_NAME="${VM_NAME:-e2e-runner-${RELEASE}}"
DATA_DIR="${AUTHD_E2E_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/authd-e2e-tests}"
IMAGE="${DATA_DIR}/${RELEASE}/${VM_NAME}.qcow2"
source e2e-tests/vm/lib/libprovision.sh
force_create_snapshot authd-google-prepared
```

Tell the tests to restore that snapshot:

```bash
E2E_TEST_SNAPSHOT=authd-google-prepared \
  ./e2e-tests/run-tests.sh \
    --broker authd-google --release noble \
    e2e-tests/tests/login_gdm.robot
```

`E2E_TEST_SNAPSHOT` is used both to start a stopped VM and before each test, so
tests still start from a clean copy of the prepared state. After a successful
run, the runner stops the VM; the next run restores the named snapshot
automatically. The setting overrides suite-specific snapshots, including the
stable snapshots used by migration tests, so use it only with compatible test
suites. Leave it unset for normal isolated and CI runs.

## Running in GitHub CI

By default, GitHub CI runs the end-to-end tests against `authd-msentraid` on all
supported Ubuntu releases (`noble`, `resolute`, and `devel`), using the complete
test suite and the authd package and broker snap built from the current branch.
All other packages are updated from the [authd-edge PPA][authd-edge-ppa].

The Entra test user needs a separate registered OATH authenticator for each
release. CI uses `E2E_MSENTRA_TOTP_SECRET_NOBLE` for noble,
`E2E_MSENTRA_TOTP_SECRET_RESOLUTE` for resolute, and
`E2E_MSENTRA_TOTP_SECRET_DEVEL` for devel.

Different releases use separate TOTP streams. Same-release runs are not queued
across branches; overlapping submissions can still replay a code. The Entra
password-and-MFA tests wait for a rejection or confirmed login before deciding
whether to retry. They submit up to three codes, generating a different one
after each rejection.

Migration suites start with the last stable authd and broker releases before
installing the selected authd package or snap. To use locally built packages in
those suites, set `AUTHD_DEB` and `BROKER_SNAP` to their host paths when
running `run-tests.sh`. `APT_SOURCE` selects the source for all packages except
authd, and `AUTHD_APT_SOURCE` independently selects the authd source. Both
variables accept the three authd PPA names or an Ubuntu archive suite.
`APT_SOURCE_BASE` selects the Ubuntu archive suite for the stable baseline of
all packages except authd. `AUTHD_APT_SOURCE_BASE` independently selects the
source for the stable authd baseline. If it is unset, the stable authd PPA is
used when it publishes the VM's Ubuntu suite; otherwise, the matching Ubuntu
archive suite is used.

The E2E workflow runs for a pull request only when it has the `e2e-tests` label.
The pull request template contains commented examples for selecting Ubuntu
releases, brokers, test suites, test cases, and package sources. Copy the
relevant line into the visible part of the pull request description to enable
it; leave it commented to use the default.

To update all packages except authd from an Ubuntu archive suite, add an
`e2e-apt-source:` line with the full suite name. For example:

```text
e2e-apt-source: resolute-proposed
```

To install authd from a PPA or archive suite independently, add an
`e2e-authd-apt-source:` line:

```text
e2e-authd-apt-source: resolute-updates
```

Either marker also accepts `authd`, `authd-edge`, or `authd-dev` to select the
stable, edge, or development PPA. PPA selections apply to every release. An
archive selection is used only by the matrix job whose Ubuntu release matches
the suite prefix.

To test migration from an archive update suite to a proposed suite with authd
from a different source, add both base markers:

```text
e2e-apt-source-base: resolute-updates
e2e-authd-apt-source-base: authd
e2e-apt-source: resolute-proposed
e2e-authd-apt-source: authd-edge
```

`e2e-apt-source-base` must be an Ubuntu archive suite. The authd base marker
accepts an authd PPA or an Ubuntu archive suite. The archive sources are used
only by the matrix job whose Ubuntu release matches the suite prefix (the
`devel` job is matched using the current Ubuntu codename, not the literal
`devel` label); other release jobs use their defaults. If
`e2e-authd-apt-source` is omitted, the branch-built authd package remains the
package under test.

To run only selected end-to-end test suites, add an `e2e-tests:` line to the
pull request description, followed by a space- or comma-separated list of suite
filenames. Repeat the marker to select more than one suite:

```text
e2e-tests: login_gdm.robot
e2e-tests: login.robot
```

To run only selected test cases from the selected suites, add one or more
`e2e-test-case:` lines with the exact Robot test case names:

```text
e2e-tests: force_access_check_with_provider.robot
e2e-test-case: Test second login succeeds with force_access_check_with_provider enabled
```

To run the tests against only selected Ubuntu releases, add an
`e2e-ubuntu-releases:` line to the pull request description. It accepts a
space- or comma-separated list of supported release names:

```text
e2e-ubuntu-releases: noble
```

To run the tests against only selected brokers, add an `e2e-brokers:` line to
the pull request description, followed by a space- or comma-separated list of
`google`/`authd-google` and/or `msentraid`/`authd-msentraid`:

```text
e2e-brokers: google
```

Editing the pull request description does not automatically re-run the
workflow. If you change an `e2e-ubuntu-releases:`, `e2e-tests:`,
`e2e-test-case:`, `e2e-brokers:`, `e2e-apt-source:`, or
`e2e-authd-apt-source:`, `e2e-apt-source-base:`, or
`e2e-authd-apt-source-base:` line after the workflow has already run, re-run the
workflow. It fetches the current pull request description from GitHub. If you
start the workflow with `workflow_dispatch`, use its separate
`e2e-ubuntu-releases`, `e2e-brokers`, `e2e-tests`, `e2e-test-case`,
`e2e-apt-source`, `e2e-authd-apt-source`, `e2e-apt-source-base`, and
`e2e-authd-apt-source-base`
inputs instead.

[yarf]: https://github.com/canonical/yarf
[authd-stable-ppa]: https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd
[authd-edge-ppa]: https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge
[authd-dev-ppa]: https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-dev
