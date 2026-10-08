---
myst:
  html_meta:
    "description lang=en": "General guidelines on contributing to authd."
---

(contribute)=
# General contribution guidelines

A big welcome and thank you for considering making a contribution to authd and Ubuntu! It’s people like you that help make these products a reality for users in our community.

By agreeing to follow these guidelines the contribution process should be easy and effective for everyone involved. This also communicates that you agree to respect the time of the developers working on this project. In return, we will reciprocate that respect by addressing your issues, assessing proposed changes and helping you finalize your pull requests.

These are mostly guidelines, not rules. Use your best judgment and feel free to propose changes to this document in a pull request.

## Code of conduct

We take our community seriously, holding ourselves and other contributors to high standards of communication. By contributing to this project you agree to uphold the Ubuntu community [Code of Conduct](https://ubuntu.com/community/ethos/code-of-conduct).

## Getting started

Contributions are made to this project via Issues and Pull Requests (PRs). These are some general guidelines that cover both:

* To report security vulnerabilities, use the advisories page of the repository and not a public bug report. Please use [launchpad private bugs](https://bugs.launchpad.net/ubuntu/+source/authd/+filebug), which is monitored by our security team. On an Ubuntu machine, it's best to use `ubuntu-bug authd` to collect relevant information.
* General issues or feature requests should be reported to the [GitHub project](https://github.com/canonical/authd/issues)
* Existing Issues and PRs should be searched for on the [project's repository](https://github.com/canonical/authd) before creating your own.
* While we work hard to ensure that issues are handled in a timely manner, it can take time to investigate the root cause. A friendly ping in the comment thread to the submitter or a contributor can help draw attention if your issue is blocking.
* If you've never contributed before, see [this post on ubuntu.com](https://ubuntu.com/community/contribute) for resources and tips on how to get started.

### Issues

Issues can be used to report problems with the software, request a new feature or discuss potential changes before a PR is created. When you [create a new Issue](https://github.com/canonical/authd/issues), a template will be loaded that will guide you through collecting and providing the information that we need to investigate.

If you find an Issue that addresses the problem you're having, please add your own reproduction information to the existing issue rather than creating a new one. Adding a [reaction](https://github.blog/2016-03-10-add-reactions-to-pull-requests-issues-and-comments/) can also help by indicating to our maintainers that a particular problem is affecting more than just the reporter.

### Pull requests

PRs to our project are always welcome and can be a quick way to get your fix or improvement slated for the next release. In general, PRs should:

* Only fix/add the functionality in question **OR** address wide-spread whitespace/style issues, not both.
* Add unit or integration tests for fixed or changed functionality.
* Address a single concern in the least possible number of changed lines.
* Include documentation in the repo or on our [docs site](https://ubuntu.com/docs/authd/stable-docs/).
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

:::{note}
PRs will trigger unit and integration tests with and without race detection, linting and formatting validations, static and security checks, and freshness of generated files verification. All these tests must pass before any merge into the main branch.
:::

The authd documentation is published in **edge-docs** and **stable-docs** versions. Only the edge version is updated when documentation changes are merged into the main branch.
If a documentation change should be applied to the stable documentation *before* the next release, create a separate PR
against the `stable-docs` branch after your main PR has been merged, with the changes to the documentation cherry-picked
from your main PR.

## Contributing to the code

See [Contributing to the code](/contributing/code.md) for the build dependencies, how to build and run each binary, and how to run the test suite.

## Contributing to the documentation

See [Contributing to the documentation](/contributing/docs.md) for how to build and test the documentation locally, and how to join the Canonical Open Documentation Academy.

## Contributor License Agreement

It is a requirement that you sign the [Contributor License Agreement](https://ubuntu.com/legal/contributors) in order to contribute to this project.
You only need to sign this once and if you have previously signed the agreement when contributing to other Canonical projects you will not need to sign it again.

An automated test is executed on PRs to check if this agreement has been accepted.

## Getting help

Join us in the [Ubuntu Community](https://discourse.ubuntu.com/c/desktop/8) and post your question there with a descriptive tag.

```{toctree}
:maxdepth: 1
:hidden:

self
code
docs
```
