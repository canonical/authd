

<!--
E2E tests in CI

The E2E workflow runs for a pull request only when it has the `e2e-tests`
label. Copy the relevant line(s) below into the visible part of the pull
request description to override the defaults. Leave these examples commented
to use the defaults:

- the `authd-msentraid` broker
- all supported Ubuntu releases
- the complete test suite
- all test cases in the selected suites
- the branch-built authd package
- the matching Ubuntu `-proposed` pocket and `authd-edge` PPA for packages other than authd
- optional Ubuntu archive suites for the matching release

e2e-brokers: google
e2e-ubuntu-releases: noble
e2e-tests: allowed_users.robot login_gdm.robot
e2e-test-case: Test login with GDM
e2e-authd-apt-source: resolute-proposed
e2e-apt-source: resolute-proposed
e2e-apt-source: authd-edge
e2e-apt-source-base: resolute-updates
e2e-authd-apt-source-base: authd

The `e2e-ubuntu-releases` marker limits the Ubuntu releases tested by the
workflow. It accepts a space- or comma-separated list of `noble`, `resolute`,
and `devel`; leave it commented to test all supported releases.

The `e2e-tests` marker accepts space- or comma-separated suite filenames or
shell-style glob patterns such as `login*.robot`. Leave the marker commented to
run the complete suite.

The `e2e-test-case` marker selects the exact Robot test case name. Repeat the
marker to select more than one test case.

The `e2e-authd-apt-source` marker adds APT sources for authd. It accepts
`authd`, `authd-edge`, or `authd-dev` to select the respective PPA, or an
archive suite such as `resolute-updates` or `resolute-proposed`. Repeat the
marker to add more sources. If no source applies to a release job, the
branch-built authd package remains the package under test. PPA sources apply to
every Ubuntu release; archive suites apply only to matching release jobs.

The `e2e-apt-source` marker adds APT sources for every package except authd.
It accepts the same PPA names and Ubuntu archive suites and can be repeated to
add more sources. With no marker, the matching Ubuntu `-proposed` pocket and
the `authd-edge` PPA are added to the VM's default archive suites. When the
marker is present, its values replace those extra defaults; the VM's default
archive suites remain enabled. APT installs the highest available package
version across the enabled sources. PPA values apply to every release, archive
suite values apply only to matching release jobs, and jobs with no applicable
values use the default sources.

The `e2e-apt-source-base` marker selects the source for the stable migration
baseline for all packages except authd.

The `e2e-authd-apt-source-base` marker selects the source for the stable
migration baseline for authd. If it's omitted, authd uses the stable `authd`
PPA when it publishes the selected Ubuntu suite, and falls back to the archive
otherwise.

The target source markers are independent. For example, these lines add
`resolute-proposed` and `authd-edge` for system packages, while adding the same
two sources as candidates for authd:

```text
e2e-apt-source: resolute-proposed
e2e-apt-source: authd-edge
e2e-authd-apt-source: resolute-proposed
e2e-authd-apt-source: authd-edge
```

The base source markers select the sources for the stable migration baseline.
`e2e-apt-source-base` selects the Ubuntu archive suite for all packages except
authd, while `e2e-authd-apt-source-base` selects the authd source. If the
authd base marker is omitted, authd uses the stable `authd` PPA when it
publishes the selected Ubuntu suite, and falls back to that suite's archive
otherwise. For example, the markers at the top of this template test a
migration from system packages in `resolute-updates` and stable authd from the
`authd` PPA to system packages in `resolute-proposed` and `authd-edge`, with
authd from `resolute-proposed`.
-->
