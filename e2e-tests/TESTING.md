# End-to-end tests

The end-to-end tests are implemented using [YARF](https://github.com/canonical/yarf).
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

For each broker you want to test, copy the corresponding template and fill in
your credentials:

```bash
# For authd-google:
cp e2e-tests/e2e-tests-google.env.template e2e-tests/e2e-tests-google.env

# For authd-msentraid:
cp e2e-tests/e2e-tests-msentraid.env.template e2e-tests/e2e-tests-msentraid.env
```

These files are gitignored.

For VM provisioning, also copy `e2e-tests/vm/config.env.template` to
`e2e-tests/vm/config.env` and set your SSH public key path (and optionally the
default Ubuntu release and VM name prefix).

### 3. Provision the VM

```bash
./e2e-tests/vm/provision.sh --broker <broker> --release <release>
```

This sets up a libvirt VM with Ubuntu, installs authd and the broker, and
creates the snapshots required by the tests. By default, authd is installed from
the edge PPA and the broker from the edge channel snap. Use `--authd-deb` to
install a locally built authd package, or `--broker-snap` to install a locally
built broker snap. Run `./e2e-tests/vm/provision.sh --help` for all available
options, including `--force` to reprovision.

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
