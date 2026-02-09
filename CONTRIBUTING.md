<!-- Include start contributing intro -->
# Contributing to authd

A big welcome and thank you for considering making a contribution to authd and Ubuntu! It’s people like you that help make these products a reality for users in our community.

By agreeing to follow these guidelines the contribution process should be easy and effective for everyone involved. This also communicates that you agree to respect the time of the developers working on this project. In return, we will reciprocate that respect by addressing your issues, assessing proposed changes and helping you finalize your pull requests.

These are mostly guidelines, not rules. Use your best judgment and feel free to propose changes to this document in a pull request.

<!-- Include end contributing intro -->
## Quicklinks

- [Contributing to authd](#contributing-to-authd)
  - [Quicklinks](#quicklinks)
  - [Code of conduct](#code-of-conduct)
  - [Getting started](#getting-started)
    - [Issues](#issues)
    - [Pull requests](#pull-requests)
  - [Contributing to the code](#contributing-to-the-code)
    - [Required dependencies](#required-dependencies)
    - [Building and running the binaries](#building-and-running-the-binaries)
      - [Building the Debian package from source](#building-the-debian-package-from-source)
      - [Building authd only](#building-authd-only)
      - [Building the PAM module only](#building-the-pam-module-only)
      - [Building the NSS module only](#building-the-nss-module-only)
    - [About the test suite](#about-the-testsuite)
      - [Tests with dependencies](#tests-with-dependencies)
    - [Code style](#code-style)
  - [Contributing to the documentation](#contributing-to-the-documentation)
    - [Building the documentation](#building-the-documentation)
    - [Testing the documentation](#testing-the-documentation)
    - [Open Documentation Academy](#open-documentation-academy)
  - [Contributor License Agreement](#contributor-license-agreement)
  - [Getting help](#getting-help)

<!-- Include start contributing main -->
## Code of conduct

We take our community seriously, holding ourselves and other contributors to high standards of communication. By contributing to this project you agree to uphold the Ubuntu community [Code of Conduct](https://ubuntu.com/community/ethos/code-of-conduct).

## Getting Started

Contributions are made to this project via Issues and Pull Requests (PRs). These are some general guidelines that cover both:

* To report security vulnerabilities, use the advisories page of the repository and not a public bug report. Please use [launchpad private bugs](https://bugs.launchpad.net/ubuntu/+source/authd/+filebug), which is monitored by our security team. On an Ubuntu machine, it’s best to use `ubuntu-bug authd` to collect relevant information. <!-- FIXME: snap? -->
* General issues or feature requests should be reported to the [GitHub Project](https://github.com/canonical/authd/issues)
* If you've never contributed before, see [this post on ubuntu.com](https://ubuntu.com/community/contribute) for resources and tips on how to get started.
* Existing Issues and PRs should be searched for on the [project's repository](https://github.com/canonical/authd) before creating your own.
* While we work hard to ensure that issues are handled in a timely manner, it can take time to investigate the root cause. A friendly ping in the comment thread to the submitter or a contributor can help draw attention if your issue is blocking.

### Issues

Issues can be used to report problems with the software, request a new feature or discuss potential changes before a PR is created. When you [create a new Issue](https://github.com/canonical/authd/issues), a template will be loaded that will guide you through collecting and providing the information that we need to investigate.

If you find an Issue that addresses the problem you're having, please add your own reproduction information to the existing issue rather than creating a new one. Adding a [reaction](https://github.blog/2016-03-10-add-reactions-to-pull-requests-issues-and-comments/) can also help by indicating to our maintainers that a particular problem is affecting more than just the reporter.

### Pull Requests

PRs to our project are always welcome and can be a quick way to get your fix or improvement slated for the next release. In general, PRs should:

* Only fix/add the functionality in question **OR** address wide-spread whitespace/style issues, not both.
* Add unit or integration tests for fixed or changed functionality.
* Address a single concern in the least possible number of changed lines.
* Include documentation in the repo or on our [docs site](https://documentation.ubuntu.com/authd/stable/).
* Be accompanied by a complete Pull Request template (loaded automatically when a PR is created).

For changes that address core functionality or that would require breaking changes (e.g. a major release), it's best to open an Issue to discuss your proposal first. This is not required but can save time when creating and reviewing changes.

In general, we follow the ["fork-and-pull" Git workflow](https://github.com/susam/gitpr):

1. Fork the repository to your own Github account.
1. Clone the fork to your machine.
1. Create a branch locally with a succinct but descriptive name.
1. Commit changes to that branch.
1. Follow any formatting and testing guidelines specific to this repo.
1. Push changes to your fork.
1. Open a PR in our repository and follow the PR template so that we can efficiently review the changes.

> PRs will trigger unit and integration tests with and without race detection, linting and formatting validations, static and security checks, and freshness of generated files verification. All these tests must pass before any merge into the main branch.

Once merged into the main branch, `po` files and any documentation change will be automatically updated. Updates to these files are therefore not necessary in the pull request itself, which helps minimize diff review.

The authd documentation is published in **edge-docs** and **stable-docs** versions. Only the edge version is updated when documentation changes are merged into the main branch.
If a documentation change should be applied to the stable documentation *before* the next release, create a separate PR
against the `stable-docs` branch after your main PR has been merged, with the changes to the documentation cherry-picked
from your main PR.

## Contributing to the code

### Required dependencies

This project has several build dependencies. You can install these dependencies from the top of the source tree using the `apt` command as follows:

```shell
sudo apt update
sudo apt build-dep .
sudo apt install devscripts
```

### Building and running the binaries

The project consists of the following binaries:

* `authd`: The main authentication service.
* `pam_authd.so`: A PAM native module (used by GDM).
* `pam_authd_exec.so`, `authd-pam`: A PAM module and its helper executable (used by other PAM applications).
* `libnss_authd.so`: An NSS module.

The project can be built as a Debian package. This process will compile all the binaries, run the test suite and produce the Debian packages.

Alternatively, for development purposes, each binary can be built manually and separately.

#### Building the Debian package from source

Building the Debian package from source is the most straightforward and standard method for compiling the binaries and running the test suite. To do this, run the following commands from the top of the source tree:

> This is required to vendorize the Rust crates and must be done only once.

```shell
sudo apt install libssl-dev
cargo install cargo-vendor-filterer
```

Then build the Debian package:

```shell
debuild --prepend-path=${HOME}/.cargo/bin
```

The Debian packages are available in the parent directory.

#### Building authd only

To build `authd` only, run the following command from the top of the source tree:

```shell
go build ./cmd/authd
```

The built binary will be found in the current directory. The daemon can be run directly from this binary without installing it on the system.

#### Building the PAM module only

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
go build -tags pam_binary_exec -o ./pam/authd-pam ./pam
```

This last command will produce two libraries (`./pam/pam_authd.so` and `./pam/go-exec/pam_authd_exec.so`) and an executable (`./pam/authd-pam`).

These modules must be copied to `/usr/lib/$(gcc -dumpmachine)/security/` while the executable must be copied to `/usr/libexec/authd-pam`.

For further information about the PAM module architecture and testing see the
[PAM Hacking](https://github.com/canonical/authd/blob/main/pam/Hacking.md) page.

#### Building the NSS module only

To build the NSS module, from the top of the source tree run the command:

```shell
cargo build
```

This will build a debug release of the NSS module.

The library resulting from the build is located in `./target/debug/libnss_authd.so`. This module must be copied to `/usr/lib/$(gcc -dumpmachine)/libnss_authd.so.2`.

### About the test suite

The project includes a comprehensive test suite made of unit and integration tests. All the tests must pass before the review is considered. If you have troubles with the test suite, feel free to mention it in your PR description.

You can run all tests with: `go test ./...` (add the `-race` flag for race detection).

Every package has a suite of at least package-level tests. They may integrate more granular unit tests for complex functionalities. Integration tests are located in `./pam/integration-tests` for the PAM module and `./nss/integration-tests` for the NSS module.

The test suite must pass before merging the PR to our main branch. Any new feature, change or fix must be covered by corresponding tests.

#### Tests with dependencies

Some tests, such as the [PAM CLI tests](https://github.com/canonical/authd/blob/5ba54c0a573f34e99782fe624b090ab229798fc3/pam/integration-tests/integration_test.go#L21), use external tools such as [VHS](https://github.com/charmbracelet/vhs)
to record and run the tape files needed for the tests. Those tools are not included in the project dependencies and must be installed manually.

Information about these tools and their usage will be linked below:

- [VHS](https://github.com/charmbracelet/vhs?tab=readme-ov-file#tutorial): tutorial on using VHS as a CLI-based video recorder

### Code style

This project follow the Go code-style. For more detailed information about the code style in use, please check <https://google.github.io/styleguide/go/>.

## Contributing to the documentation

You can contribute to the documentation in various ways.

At the top of each page in the documentation, there is a **Give feedback**
button. If you find an issue in the documentation, clicking this button will
open an Issue submission on GitHub for the specific page.

For minor changes, such as fixing a single typo, you can click the **pencil**
icon at the top right of any page. This will open up the source file in GitHub so
that you can make edits directly.

For more significant changes to the content or organization of the
documentation, you should create your own fork and follow the steps
outlined in the section on [pull requests](#pull-requests).

### Building the documentation

After cloning your fork, change into the `/docs/` directory.
The documentation is written in markdown files grouped under
[Diátaxis](https://diataxis.fr/) categories.

A makefile is used to preview and test the documentation locally.
To view all the possible commands, run `make` without arguments.

The command `make run` will serve the documentation at port `8000` on
`localhost`. You can then preview the documentation in your browser and the
preview will automatically update with each change that you make.

To clean the build environment at any point, run `make clean`.

When you submit a PR, there are automated checks for typos and broken links.
Please run the tests locally before submitting the PR to save yourself and your
reviewers time.

### Testing the documentation

Automatic checks will be run on any PR relating to documentation to verify
spelling and the validity of links. Before submitting a PR, you can check for
any issues locally:

- Check the spelling: `make spelling`
- Check the validity of links: `make linkcheck`

Doing these checks locally is good practice. You are less likely to run into
failed CI checks after your PR is submitted and the reviewer of your PR can
more quickly focus on the substance of your contribution.

If the documentation builds, your PR will generate a preview of the
documentation on Read the Docs. This preview appears as a check in the CI.
Click on the check to open the preview and confirm that your changes have been
applied successfully.

### Open Documentation Academy

authd is a proud member of the [Canonical Open Documentation
Academy](https://github.com/canonical/open-documentation-academy) (CODA).

CODA is an initiative to encourage open source contributions from the
community, and to provide help, advice and mentorship to people making their
first contributions.

A key aim of the initiative is to lower the barrier to successful open-source
software contributions by making documentation into the gateway, and it’s a
great way to make your first open source contributions to projects like authd.

The best way to get started is to take a look at our [project-related
documentation
tasks](https://github.com/canonical/open-documentation-academy/issues) and read
our [Getting started
guide](https://discourse.ubuntu.com/t/getting-started/42769). Tasks typically
include testing and fixing documentation pages, updating outdated content, and
restructuring large documents. We'll help you see those tasks through to
completion.

You can get involved the with the CODA community through:

* The [discussion forum](https://discourse.ubuntu.com/c/community/open-documentation-academy/166) on the Ubuntu Community Hub
* The [Matrix channel](https://matrix.to/#/#documentation:ubuntu.com) for interactive chat
* [Fosstodon](https://fosstodon.org/@CanonicalDocumentation) for the latest updates and events

## Contributor License Agreement

It is a requirement that you sign the [Contributor License Agreement](https://ubuntu.com/legal/contributors) in order to contribute to this project.
You only need to sign this once and if you have previously signed the agreement when contributing to other Canonical projects you will not need to sign it again.

An automated test is executed on PRs to check if this agreement has been accepted.

<!-- TODO: add license. -->
<!-- This project is covered by [THIS LICENSE](LICENSE). -->

## Getting help

Join us in the [Ubuntu Community](https://discourse.ubuntu.com/c/desktop/8) and post your question there with a descriptive tag.
<!-- Include end contributing main -->
