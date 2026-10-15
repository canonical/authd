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
creates the snapshots required by the tests. By default, the VM's normal
archive suites, the matching `-proposed` suite, and the
[authd-edge PPA][authd-edge-ppa] are enabled. APT selects the highest available
package version across those sources. Authd comes from the local package when
`--authd-deb` is supplied, and the broker comes from the edge channel snap.

Use `--apt-source <source>` to add a source for all packages except authd, or
`--authd-apt-source <source>` to add a source for authd. Both options accept
`authd`, `authd-edge`, `authd-dev`, or an Ubuntu archive suite, and can be
repeated to add more sources. If any `--apt-source` options are supplied, they
replace the default `-proposed` and `authd-edge` sources. Use `--authd-deb` for
a local authd package; it cannot be combined with `--authd-apt-source`. Use
`--broker-snap` to install a locally built broker snap. Run
`./e2e-tests/vm/provision.sh --help` for all available options, including
`--force` to reprovision.

APT uses the highest package version available from the enabled system
sources. Authd-only sources remain lower priority for other packages, while
authd can use the highest version from both source lists.

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
  --apt-source authd-edge \
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

### Entra Unix ID fixtures

Like the passwordless suite, `entra_unix_uid_gid_provisioning.robot` runs when
its account and fixture settings are configured and skips cases whose required
settings are missing. There is no separate enable flag. Set the directory
fixture values in the ignored `e2e-tests-msentraid.env` file using the
`E2E_UNIX_IDS_*` variables from `e2e-tests-msentraid.env.template`:

- `E2E_UNIX_IDS_UID_ATTRIBUTE` and `E2E_UNIX_IDS_GID_ATTRIBUTE`: the full
  extension property names to test.
- `E2E_UNIX_IDS_EXPECTED_UID` and `E2E_UNIX_IDS_EXPECTED_GID`: the assigned
  positive integer values.
- `E2E_UNIX_IDS_GROUP`: the remote group with that GID.
- `E2E_UNIX_IDS_EXPECTED_UGID`: optionally, that group's Entra object ID.
- `E2E_UNIX_IDS_GENERIC_GROUP`: a group without a GID for missing-attribute
  cases.
- `E2E_UNIX_IDS_NO_GID_EXPECTED_UID`: the valid UID assigned to the optional
  no-GID test account.

The short attribute names used by the suite are `Linux_UID` and `Linux_GID`.
Use full property names in the environment variables so the suite tests both
full and short-name resolution.
In GitHub CI, the optional repository variables
`E2E_UNIX_IDS_UID_SHORT_ATTRIBUTE` and `E2E_UNIX_IDS_GID_SHORT_ATTRIBUTE`
override these short names.

Configure `E2E_UNIX_IDS_USER`, `E2E_UNIX_IDS_PASSWORD`, and
`E2E_UNIX_IDS_TOTP_SECRET` in the Microsoft Entra broker's `.env` file. These
credentials belong to a member of `${configured_group}` with the expected UID;
positive tests never fall back to the shared `E2E_USER`.

For missing-attribute cases, use the shared `E2E_USER` without a UID who belongs
to `${generic_test_group}`. The optional no-GID fixture also belongs to this
group but has a valid UID. Configure its `E2E_UNIX_IDS_NO_GID_USER`,
`E2E_UNIX_IDS_NO_GID_PASSWORD`, and `E2E_UNIX_IDS_NO_GID_TOTP_SECRET` in the
`.env` file. Cases with missing fixture settings skip before VM setup.

```bash
./e2e-tests/run-tests.sh --broker authd-msentraid --release resolute \
    e2e-tests/tests/entra_unix_uid_gid_provisioning.robot
```

In GitHub CI, the suite participates in normal Entra runs and uses the same
configuration-based skipping as local runs. Configure the fixture values as
repository secrets: `E2E_MSENTRA_UNIX_IDS_UID_ATTRIBUTE`,
`E2E_MSENTRA_UNIX_IDS_GID_ATTRIBUTE`, `E2E_MSENTRA_UNIX_IDS_EXPECTED_UID`,
`E2E_MSENTRA_UNIX_IDS_EXPECTED_GID`, `E2E_MSENTRA_UNIX_IDS_EXPECTED_UGID`,
`E2E_MSENTRA_UNIX_IDS_GROUP`, `E2E_MSENTRA_UNIX_IDS_GENERIC_GROUP`, and
`E2E_MSENTRA_UNIX_IDS_NO_GID_EXPECTED_UID`. Use the
`E2E_MSENTRA_UNIX_IDS_USERNAME`, `E2E_MSENTRA_UNIX_IDS_PASSWORD`, and
`E2E_MSENTRA_UNIX_IDS_TOTP_SECRET` secrets for the positive account. For the
optional no-GID account, use `E2E_MSENTRA_UNIX_IDS_NO_GID_USERNAME`,
`E2E_MSENTRA_UNIX_IDS_NO_GID_PASSWORD`, and
`E2E_MSENTRA_UNIX_IDS_NO_GID_TOTP_SECRET`. The reusable workflows forward
these settings and credentials to the runner.

## Running in GitHub CI

By default, GitHub CI runs the end-to-end tests against `authd-msentraid` on all
supported Ubuntu releases (`noble`, `resolute`, and `devel`), using the complete
test suite and the authd package and broker snap built from the current branch.
The VM's default archive suites, the matching Ubuntu release's `-proposed`
pocket, and the `authd-edge` PPA are enabled for system packages. APT installs
the highest available package version from those sources.
Migration suites start with the last stable authd and broker releases before
installing the selected authd package or snap. To use locally built packages in
those suites, set `AUTHD_DEB` and `BROKER_SNAP` to their host paths when
running `run-tests.sh`. `APT_SOURCE` and `AUTHD_APT_SOURCE` accept
comma- or space-separated lists of the three authd PPA names and/or Ubuntu
archive suites. An unset `APT_SOURCE` uses the matching `-proposed` suite and
`authd-edge`; an unset `AUTHD_APT_SOURCE` uses the system sources for local APT
installs. GitHub CI continues to use the branch-built authd package unless
`e2e-authd-apt-source` is set.
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

The `e2e-apt-source:` marker adds one or more sources for all packages except
authd. Each value can be an authd PPA name or a full Ubuntu archive suite.
Repeat the marker to add more sources. When the marker is absent, the workflow
adds the release's `-proposed` suite and the `authd-edge` PPA. When it is
present, its values replace those extra defaults; the VM's normal archive
suites stay enabled. For example, to add both sources explicitly:

```text
e2e-apt-source: resolute-proposed
e2e-apt-source: authd-edge
```

The `e2e-authd-apt-source:` marker adds sources for authd and accepts the same
PPA names and Ubuntu suites. Repeat it to add more sources. If omitted, the
branch-built authd package remains under test for jobs with no matching source.
To add two authd sources:

```text
e2e-authd-apt-source: resolute-proposed
e2e-authd-apt-source: authd-edge
```

APT selects the highest available version across enabled sources. PPA sources
apply to every release; archive suites apply only to matching release jobs.
Jobs with no applicable system sources use their default `-proposed` suite and
`authd-edge` PPA.

To test migration from an archive update suite to a proposed suite with authd
from a different source, add both base markers:

```text
e2e-apt-source-base: resolute-updates
e2e-authd-apt-source-base: authd
e2e-apt-source: resolute-proposed
e2e-apt-source: authd-edge
e2e-authd-apt-source: authd-edge
```

`e2e-apt-source-base` must be an Ubuntu archive suite. The authd base marker
accepts an authd PPA or an Ubuntu archive suite. Archive sources are used only
by the matrix job whose Ubuntu release matches the suite prefix (the `devel`
job is matched using the current Ubuntu codename, not the literal `devel`
label). If `e2e-authd-apt-source` is omitted, the branch-built authd package
remains the package under test.

To run only selected end-to-end test suites, add an `e2e-tests:` line to the
pull request description, followed by a space- or comma-separated list of suite
filenames or shell-style glob patterns (`*`, `?`, and bracket expressions).
Patterns match `.robot` files directly under `e2e-tests/tests`; a pattern that
matches no suites fails the workflow. Repeat the marker to select more than
one suite:

```text
e2e-tests: login*.robot
e2e-tests: migration*.robot
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
`e2e-test-case:`, `e2e-brokers:`, `e2e-apt-source:`,
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
