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
creates the snapshots required by the tests. By default, authd is installed
from the [authd-edge PPA][authd-edge-ppa] and the broker from the edge channel
snap. Use `--authd-deb` to install a locally built authd package, `--authd-ppa
<ppa>` to select a different PPA for authd and its dependencies, or
`--broker-snap` to install a locally built broker snap. Run
`./e2e-tests/vm/provision.sh --help` for all available options, including
`--force` to reprovision.

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
the full suite. Run `./e2e-tests/run-tests.sh --help` for all available options,
including `--rerunfailed` and `--output-dir`.

## Running in GitHub CI

By default, GitHub CI runs the end-to-end tests against both `authd-google` and
`authd-msentraid`, using the complete test suite and packages from the
[authd-edge PPA][authd-edge-ppa].

The E2E workflow runs for a pull request only when it has the `e2e-tests` label.
The pull request template contains commented examples for selecting brokers,
test suites, and the authd PPA. Copy the relevant line into the visible part of
the pull request description to enable it; leave it commented to use the
default.

To install authd and its dependencies from the [authd-dev PPA][authd-dev-ppa]
instead, add `e2e-ppa: authd-dev` to the pull request description.

To run only selected end-to-end test suites, add an `e2e-tests:` line to the
pull request description, followed by a space- or comma-separated list of suite
filenames:

```text
e2e-tests: login_gdm.robot login.robot
```

To run the tests against only selected brokers, add an `e2e-brokers:` line to
the pull request description, followed by a space- or comma-separated list of
`google`/`authd-google` and/or `msentraid`/`authd-msentraid`:

```text
e2e-brokers: google
```

Editing the pull request description does not automatically re-run the
workflow. If you change an `e2e-tests:`, `e2e-brokers:`, or `e2e-ppa:` line
after the workflow has already run, re-run the workflow. It fetches the current
pull request description from GitHub. If you start the workflow with
`workflow_dispatch`, use its separate `e2e-brokers`, `e2e-tests`, and `e2e-ppa`
inputs instead.

[yarf]: https://github.com/canonical/yarf
[authd-edge-ppa]: https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-edge
[authd-dev-ppa]: https://launchpad.net/~ubuntu-enterprise-desktop/+archive/ubuntu/authd-dev
