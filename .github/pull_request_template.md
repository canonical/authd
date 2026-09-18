

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
- the `authd-edge` PPA for packages other than authd

e2e-brokers: google
e2e-ubuntu-releases: noble
e2e-tests: allowed_users.robot login_gdm.robot
e2e-test-case: Test login with GDM
e2e-authd-apt-source: resolute-proposed
e2e-apt-source: authd-dev

The `e2e-ubuntu-releases` marker limits the Ubuntu releases tested by the
workflow. It accepts a space- or comma-separated list of `noble`, `resolute`,
and `devel`; leave it commented to test all supported releases.

The `e2e-authd-apt-source` marker selects the PPA or Ubuntu archive suite used
to install authd. It accepts `authd`, `authd-edge`, or `authd-dev` to select the
respective PPA, or an archive suite such as `resolute-updates` or
`resolute-proposed`. If it is omitted, the branch-built authd package remains
the package under test. PPA selections apply to every Ubuntu release; archive
selections apply only to matching release jobs, while other jobs use their
defaults.

The `e2e-apt-source` marker independently selects the PPA or Ubuntu archive
suite used for every package except authd. It accepts `authd`, `authd-edge`, or
`authd-dev` to select the respective PPA, or an archive suite such as
`resolute-updates` or `resolute-proposed`. It defaults to `authd-edge`.

The `e2e-test-case` marker selects the exact Robot test case name. Repeat the
marker to select more than one test case.

The two source markers are independent. For example, `e2e-apt-source:
resolute-proposed` updates all other packages from proposed while
`e2e-authd-apt-source: authd-edge` installs authd from the edge PPA.
-->
