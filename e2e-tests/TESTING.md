# End-to-end tests

The end-to-end tests are implemented using [YARF][yarf].
They cover a wide range of scenarios, both for authd and the brokers.

## Setting up the environment

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
`--rerunfailed` and `--output-dir`.

## Running in GitHub CI

By default, GitHub CI runs the end-to-end tests against `authd-msentraid` on all
supported Ubuntu releases (`noble`, `resolute`, and `devel`), using the complete
test suite and the authd package and broker snap built from the current branch.
All other packages are updated from the [authd-edge PPA][authd-edge-ppa].
Migration suites start with the last stable authd and broker releases before
installing the selected authd package or snap. To use locally built packages in
those suites, set `AUTHD_DEB` and `BROKER_SNAP` to their host paths when
running `run-tests.sh`. `APT_SOURCE` selects the source for all packages except
authd, and `AUTHD_APT_SOURCE` independently selects the authd source. Both
variables accept the three authd PPA names or an Ubuntu archive suite.

The E2E workflow runs for a pull request only when it has the `e2e-tests` label.
The pull request template contains commented examples for selecting Ubuntu
releases, brokers, test suites, test cases, and the two package sources. Copy
the relevant line into the visible part of the pull request description to
enable it; leave it commented to use the default.

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
the suite prefix (the `devel` job is matched using the current Ubuntu codename,
not the literal `devel` label); other release jobs use their defaults. If
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
`e2e-authd-apt-source:` line after the workflow has already run, re-run the
workflow. It fetches the current pull request description from GitHub. If you
start the workflow with `workflow_dispatch`, use its separate
`e2e-ubuntu-releases`, `e2e-brokers`, `e2e-tests`, `e2e-test-case`,
`e2e-apt-source`, and `e2e-authd-apt-source`
inputs instead.

[yarf]: https://github.com/canonical/yarf
[authd-stable-ppa]: https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd
[authd-edge-ppa]: https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge
[authd-dev-ppa]: https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-dev
