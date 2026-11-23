# Contributing to authd

The full contributing guide, including how to build and test the code and
the documentation, is published on our documentation site:

* [Contributing to authd](https://ubuntu.com/docs/authd/edge-docs/contributing/)
* [Contributing to the code](https://ubuntu.com/docs/authd/edge-docs/contributing/code/)
* [Contributing to the documentation](https://ubuntu.com/docs/authd/edge-docs/contributing/docs/)

The same pages are also available as source files in this repository, under
[`docs/contributing/`](docs/contributing/).

### Upstream and competitor watch

The private repository has a [GitHub Agentic Workflow](https://github.github.com/gh-aw/)
in `.github/workflows/competitor-watch.md`. It runs on Mondays at 09:00 UTC
and sends an AI-generated digest to Mattermost, including source links and
possible implications for authd. GitHub may delay scheduled runs.

It checks GitHub releases, merged pull requests, and updated issues for
`himmelblau-idm/himmelblau` and `SSSD/sssd`. For FreeIPA it uses the
[primary Codeberg repository](https://codeberg.org/freeipa/freeipa), not the
GitHub mirror: releases, linked official release notes, updated issues, and
commits integrated into the default branch. FreeIPA does not enable Codeberg
pull requests, so integrated commits replace merged PRs in its digest.
New and edited issue comments provide context. Other branches, mailing lists,
blogs, and release-note edits to old releases are not monitored.

The digest also covers authentication-related issues and merge requests in
GNOME GitLab's `GNOME/gnome-shell` and `Teams/Design/os-mockups` projects,
including open/draft proposals and new or edited comments. The collector
retrieves all updated items; the agent selects relevant authentication work.
It explicitly watches the [passwordless login design](https://gitlab.gnome.org/Teams/Design/os-mockups/-/work_items/220)
and [embedded webview MR](https://gitlab.gnome.org/GNOME/gnome-shell/-/merge_requests/4033).
Notes on these watched items are checked even when their parent timestamp
has not changed. Unchanged proposals are not repeated as news. Other GNOME
projects and changes to design image files outside issues/MRs are not monitored.

To enable it:

1. Add `MATTERMOST_WEBHOOK_URL` as an Actions secret. Use an HTTPS incoming
   webhook locked to the destination channel. The hosted runner must be able
   to reach the Mattermost server.
2. Add `COPILOT_GITHUB_TOKEN` as an Actions secret. Use a fine-grained PAT owned
   by a user with an active Copilot license, with the account permission
   **Copilot Requests: Read**. See the
   [gh-aw authentication guide](https://github.github.com/gh-aw/reference/auth/).
   Inference uses that user's quota. Do not put either secret in a file.
3. Create a personal access token on **gitlab.gnome.org** with **read_api**
   scope and store it as the Actions secret `GNOME_GITLAB_TOKEN`. GNOME's
   discussion API requires authentication even for these public projects.
   The token is used only by the collection step, not the AI agent or
   Mattermost delivery. It is required for dry runs too. If the token or a
   discussion read is unavailable, that GNOME activity is reported as a gap;
   other sources can still be sent.
4. Allow Actions to run the pinned actions and to write contents on the
   `automation/competitor-watch-state` branch. Rulesets must permit the
   workflow to create and update this branch. No default-branch writes are
   needed. Merge the workflow source, generated lock file, and helper scripts
   into the default branch of `canonical/authd-private`.
5. Run **Upstream and competitor watch** from the Actions tab with `dry_run` enabled.
   Inspect the delivery job's summary. Run again with `dry_run` disabled to
   send a message. The repository/default-branch guard prevents fork runs.

The first real run saves a baseline seven days in the past. Later runs cover
everything since the last accepted message, even after failures or a long
pause. The checkpoint is a JSON file on the state branch, not an evictable
Actions cache. The delivery job advances it only after Mattermost returns
HTTP 200 with `ok`. The agent cannot access the webhook or change the
checkpoint. Runs are serialized, and delivery checks for a stale checkpoint.

FreeIPA also saves the last code snapshot's commit SHA. Subsequent runs compare
commit ranges, which includes newly integrated commits with old commit dates.
The first run uses committer dates from the initial baseline. Its snapshot is
taken during collection and can be slightly later than the digest's time
window. A rewritten history fails rather than silently resetting this cursor.

A no-news run still sends a short message. A fetch failure omits only the
affected activity group and marks the report as partial. The checkpoint is
not advanced until every group is collected, so retries can repeat activity
that was already sent. Excessive input (over 2 MB), invalid output, and
rejected threat detection also do not advance the checkpoint. Dry runs neither
send messages nor change state; they still use Copilot inference. A webhook
timeout or a failure to save after sending may cause a duplicate on retry.
Incoming webhooks cannot provide exactly-once delivery. Review failed Actions
runs before retrying.

Do not delete the state branch to recover a failed run: this would reset the
lookback and could skip news. Restore its checkpoint from branch history.
If first-time initialization creates the branch but cannot write the file,
create `competitor-watch-state.json` there with
`{"since":"<the original UTC baseline>"}` before retrying. Preserve the
`freeipa_head` field when repairing an existing checkpoint.

The agent receives public source data only; it has no repository checkout or
GitHub tools. The Mattermost credential is used only in the delivery job.
Source links are checked against the collected URLs, mentions are disabled,
and images and HTML are rejected. These controls limit side effects, but
the digest is still AI-generated and needs human judgment.

When changing the workflow, use gh-aw **v0.89.21**, run
`gh aw compile competitor-watch`, and commit the generated `.lock.yml` and
any updates to `.github/aw/actions-lock.json`. Do not edit generated YAML.
Run the focused checks:

```shell
python3 -m unittest discover -s .github/scripts -p 'test_competitor_watch.py'
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/competitor-watch.lock.yml
go run github.com/google/yamlfmt/cmd/yamlfmt@latest -lint .github
```
