---
myst:
  html_meta:
    "description lang=en": "How to build and test the authd code, PAM module, NSS module, and brokers."
---

# Contributing to the code

## Required dependencies

This project has several build dependencies. You can install these dependencies from the top of the source tree using the `apt` command as follows:

```shell
sudo apt update
sudo apt build-dep .
sudo apt install devscripts
```

## Building and running the binaries

The project consists of the following binaries:

* `authd`: The main authentication service.
* `pam_authd.so`: A PAM native module (used by GDM).
* `pam_authd_exec.so`, `authd-pam`: A PAM module and its helper executable (used by other PAM applications).
* `libnss_authd.so`: An NSS module.

The project can be built as a Debian package. This process will compile all the binaries, run the test suite and produce the Debian packages.

Alternatively, for development purposes, each binary can be built manually and separately.

### Building the Debian package from source

Building the Debian package from source is the most straightforward and standard method for compiling the binaries and running the test suite. To do this, run the following commands from the top of the source tree:

:::{note}
This is required to vendorize the Rust crates and must be done only once.
:::

```shell
sudo apt install libssl-dev
cargo install cargo-vendor-filterer
```

Then build the Debian package:

```shell
debuild --prepend-path=${HOME}/.cargo/bin
```

The Debian packages are available in the parent directory.

### Building authd only

To build `authd` only, run the following command from the top of the source tree:

```shell
go build ./cmd/authd
```

The built binary will be found in the current directory. The daemon can be run directly from this binary without installing it on the system.

### Building the PAM module only

To build the PAM module, you first need to install the tooling to hook up the Go gRPC modules to protoc.
From the top of the source tree run the following commands:

```shell
cd tools/
grep -o '_ ".*"' *.go | cut -d '"' -f 2 | xargs go install
cd ..
```

Then build the PAM module:

```shell
go generate ./pam/
go build -o ./pam/authd-pam ./pam
```

The `go generate` step produces two PAM modules (`./pam/pam_authd.so` and `./pam/go-exec/pam_authd_exec.so`).

The `go build` step produces the PAM helper executable (`./pam/authd-pam`).

These modules must be copied to `/usr/lib/$(gcc -dumpmachine)/security/` while the executable must be copied to `/usr/libexec/authd-pam`.

For further information about the PAM module architecture and testing see the
[README for the authd PAM module](https://github.com/canonical/authd/blob/main/pam/README.md).

### Building the NSS module only

To build the NSS module, from the top of the source tree run the command:

```shell
cargo build
```

This will build a debug release of the NSS module.

The library resulting from the build is located in `./target/debug/libnss_authd.so`. This module must be copied to `/usr/lib/$(gcc -dumpmachine)/libnss_authd.so.2`.

## Building the broker

The authd brokers are packaged as separate snaps that are built and released
independently from authd. The source code for the brokers is located in
`./authd-oidc-brokers` and the snap packaging files are located in `./snap`.

To build the broker snap for a specific broker variant, follow these steps from the top of the source tree:

1. Ensure that the submodules are checked out:

   ```shell
   git submodule update --init --recursive
   ```

2. Prepare the `snapcraft.yaml` for the desired broker variant:

   ```shell
   ./snap/scripts/prepare-variant --broker <broker>
   ```

   where `<broker>` is one of `oidc`, `msentraid`, or `google`.

3. Build the broker snap:

   ```shell
   snapcraft pack
   ```

When the build succeeds, the resulting `.snap` file is created in the current working directory.
You can install the locally built broker snap for development with a command such as:

```shell
snap install --dangerous ./path/to/broker.snap
```

## About the test suite

The project includes a comprehensive test suite made of unit and integration tests. All the tests must pass before the review is considered. If you have troubles with the test suite, feel free to mention it in your PR description.

You can run all tests with: `go test ./...` (add the `-race` flag for race detection).

Every package has a suite of at least package-level tests. They may integrate more granular unit tests for complex functionalities. Integration tests are located in `./pam/integration-tests` for the PAM module and `./nss/integration-tests` for the NSS module.

The test suite must pass before merging the PR to our main branch. Any new feature, change or fix must be covered by corresponding tests.

## Code style

This project follow the Go code-style. For more detailed information about the code style in use, please check <https://google.github.io/styleguide/go/>.

