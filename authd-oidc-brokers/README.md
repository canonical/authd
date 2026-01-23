# Welcome to authd OpenID Connect Broker

[actions-image]: https://github.com/ubuntu/authd-oidc-brokers/actions/workflows/ci.yml/badge.svg
[actions-url]: https://github.com/ubuntu/authd-oidc-brokers/actions?query=workflow%3ACI

[license-image]: https://img.shields.io/badge/License-GPL3.0-blue.svg

[codecov-image]: https://codecov.io/gh/ubuntu/authd-oidc-brokers/graph/badge.svg
[codecov-url]: https://codecov.io/gh/ubuntu/authd-oidc-brokers

[user-documentation-image]: https://pkg.go.dev/badge/github.com/ubuntu/authd-oidc-brokers.svg
[user-documentation-url]: https://pkg.go.dev/github.com/ubuntu/authd-oidc-brokers

[goreport-image]: https://goreportcard.com/badge/github.com/ubuntu/authd-oidc-brokers
[goreport-url]: https://goreportcard.com/report/github.com/ubuntu/authd-oidc-brokers

[![Code quality][actions-image]][actions-url]
[![License][license-image]](LICENSE)
[![Code coverage][codecov-image]][codecov-url]
[![User Documentation][user-documentation-image]][user-documentation-url]
[![Go Report Card][goreport-image]][goreport-url]

This is the code repository for authd OpenID Connect (OIDC) brokers. It is used in conjunction with Ubuntu authentication daemon [authd](https://github.com/ubuntu/authd).

This project contains specific code for different OpenID Connect providers. We build one binary for each and snap them based on build tags to integrate with the Ubuntu authentication daemon.

For general details, read the [authd documentation](https://documentation.ubuntu.com/authd/stable-docs/).

The documentation includes details on [installing](https://documentation.ubuntu.com/authd/stable-docs/howto/install-authd/) and [configuring](https://documentation.ubuntu.com/authd/stable-docs/howto/configure-authd/) authd.

## Troubleshooting

More details on troubleshooting one authd and authd brokers is available in the [authd documentation](https://documentation.ubuntu.com/authd/stable-docs/reference/troubleshooting/).

## Get involved

This is an [open source](LICENSE) project and we warmly welcome community contributions, suggestions, and constructive feedback. If you're interested in contributing, please take a look at our [Contribution guidelines](CONTRIBUTING.md) first.

- to report an issue, please file a bug report against the [authd repository](https://github.com/ubuntu/authd), using the bug template.
- for suggestions and constructive feedback, report a feature request bug report, using the proposed template.

## Get in touch

We're friendly! We have a community forum at [https://discourse.ubuntu.com](https://discourse.ubuntu.com) where we discuss feature plans, development news, issues, updates and troubleshooting.

For news and updates, follow the [Ubuntu twitter account](https://twitter.com/ubuntu) and on [Facebook](https://www.facebook.com/ubuntu).
