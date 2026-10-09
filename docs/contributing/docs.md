---
myst:
  html_meta:
    "description lang=en": "How to contribute to the authd documentation, including building and testing it locally."
---

# Contributing to the documentation

You can contribute to the documentation in various ways.

At the top of each page in the documentation, there is a **Give feedback**
button. If you find an issue in the documentation, clicking this button will
open an Issue submission on GitHub for the specific page.

For minor changes, such as fixing a single typo, you can click the **pencil**
icon at the top right of any page. This will open up the source file in GitHub so
that you can make edits directly.

For more significant changes to the content or organization of the
documentation, you should create your own fork and follow the steps
outlined in the section on [pull requests](/contributing/index.md#pull-requests).

## Building the documentation

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

## Building stable and edge versions of the documentation

authd publishes two versions of its documentation: stable-docs and edge-docs.
By default, `make run` previews the stable version.

To preview a specific version, set `READTHEDOCS_VERSION` before running `make run`:

**Edge**:

```shell
READTHEDOCS_VERSION=edge-docs make run
```

**Stable**:

```shell
READTHEDOCS_VERSION=stable-docs make run
```

Some content is conditionally rendered depending on the version. This includes
the version warning banner in the edge documentation and installation
instructions that are specific to each version.

## Testing the documentation

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

## Open Documentation Academy

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

