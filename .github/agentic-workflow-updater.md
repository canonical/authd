# Agentic workflow updater

`.github/workflows/update-agentic-workflows.yaml` runs every Monday at
02:00 UTC. It upgrades the gh-aw compiler and the generated workflows,
then opens a draft pull request on `auto-update-agentic-workflows`.

## Why it uses a GitHub App token

The updater rewrites files under `.github/workflows/`, and
`GITHUB_TOKEN` cannot be granted the Workflows permission needed to
push those files. The token must also trigger pull request checks and
sign the commits.

A fine-grained personal access token works, but it ties the job to one
maintainer account. Commits made with it are unsigned and authored
from a `users.noreply` address, so the Canonical CLA check fails.

The workflow instead mints a short-lived installation token from a
GitHub App. That token is scoped to this repository and revoked at the
end of the job. Commits are authored and signed as the App's bot user,
which the CLA check exempts.

## Setup

Create an organization-owned GitHub App, install it on
`canonical/authd`, and grant these repository permissions:

- Contents: write
- Pull requests: write
- Workflows: write

Store the App client ID in the `AGENTIC_WORKFLOW_UPDATE_APP_CLIENT_ID`
repository variable and the App private key in the
`AGENTIC_WORKFLOW_UPDATE_APP_PRIVATE_KEY` secret.

The `permission-*` inputs in the workflow can only narrow the
installation's permissions; the App must already have them.

## Order of operations

The first run after this change needs the App credentials. If they are
missing, a run with nothing to upgrade still succeeds; the first run
with changes fails at the "Mint update token" step.

1. Create and install the App, then set the variable and secret.
2. Merge the token change.
3. Run the updater once (`workflow_dispatch`) and check that the pull
   request commits are signed and authored by `<app-slug>[bot]`.
4. Delete the old `AGENTIC_WORKFLOW_UPDATE_TOKEN` secret.

## Pull request identity

New pull requests are opened by `<app-slug>[bot]`. For an already open
pull request, the opener does not change; only its branch commits are
rewritten with the bot as author.
