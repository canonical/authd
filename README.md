# authd: an authentication daemon for cloud identity providers

[actions-image]: https://github.com/canonical/authd/actions/workflows/qa.yaml/badge.svg
[actions-url]: https://github.com/canonical/authd/actions?query=workflow%3AQA

[license-image]: https://img.shields.io/badge/License-GPL3.0-blue.svg

[codecov-image]: https://codecov.io/gh/ubuntu/authd/graph/badge.svg
[codecov-url]: https://codecov.io/gh/ubuntu/authd

[reference-documentation-image]: https://pkg.go.dev/badge/github.com/canonical/authd.svg
[reference-documentation-url]: https://pkg.go.dev/github.com/canonical/authd

[goreport-image]: https://goreportcard.com/badge/github.com/canonical/authd
[goreport-url]: https://goreportcard.com/report/github.com/canonical/authd

[docs-image]: https://readthedocs.com/projects/canonical-authd/badge/?version=edge-docs
[docs-url-stable]: https://documentation.ubuntu.com/authd/stable-docs/
[docs-url-edge]: https://documentation.ubuntu.com/authd/edge-docs/

[![Code quality][actions-image]][actions-url]
[![License][license-image]](COPYING)
[![Code coverage][codecov-image]][codecov-url]
[![Go Report Card][goreport-image]][goreport-url]
[![Reference documentation][reference-documentation-image]][reference-documentation-url]

[![Documentation Status][docs-image]][docs-url-stable]

authd is an authentication daemon for cloud-based identity providers. It helps
ensure the secure management of identity and access for Ubuntu machines anywhere
in the world, on desktop and the server. authd's modular design makes it a
versatile authentication service that can integrate with multiple identity
providers. `MS Entra ID` and `Google Cloud's Identity and Access Management` are currently
supported and several other identity providers are under active development.

## Documentation

To find out more about using authd, refer to the
[official authd documentation][docs-url-stable].
If you are on an edge release then you can also read the
[edge version of the documentation][docs-url-edge],
which may include features not yet available in the stable release.

The documentation includes how-to guides on installing and configuring authd,
in addition to information about authd architecture and troubleshooting.

## Brokers

authd uses brokers to interface with cloud identity providers through a
[DBus API](https://github.com/canonical/authd/blob/HEAD/examplebroker/com.ubuntu.auth.ExampleBroker.xml).

Currently [MS Entra ID](https://learn.microsoft.com/en-us/entra/fundamentals/whatis)
and [Google IAM](https://cloud.google.com/iam/docs/overview)
are supported as identity providers.
They allow you to authenticate using MFA and the device authentication flow.

For development purposes, authd also provides an
[example broker](https://github.com/canonical/authd/tree/main/examplebroker)
to help you develop your own.

## Get involved

This is an [open source](COPYING) project and we warmly welcome community
contributions, suggestions, and constructive feedback. If you're interested in
contributing, please take a look at our [contribution guidelines](CONTRIBUTING.md)
first.

When reporting an issue you can
[choose from several templates](https://github.com/canonical/authd/issues/new/choose):

- To report an issue, please file a bug report against our repository, using the
  [report an issue](https://github.com/canonical/authd/issues/new?assignees=&labels=bug&projects=&template=bug_report.yml&title=Issue%3A+) template.
- For suggestions and constructive feedback, report a feature request bug report, using the
  [request a feature](https://github.com/canonical/authd/issues/new?assignees=&labels=feature&projects=&template=feature_request.yml&title=Feature%3A+) template.

## Get in touch

We're friendly! You can find our community forum at
[https://discourse.ubuntu.com](https://discourse.ubuntu.com)
where we discuss feature plans, development news, issues, updates and troubleshooting.
