<!--
E2E tests in CI

The E2E workflow runs for a pull request only when it has the `e2e-tests`
label. Copy the relevant line(s) below into the visible part of the pull
request description to override the defaults. Leave these examples commented
to use the defaults:

- both brokers (`authd-google` and `authd-msentraid`)
- the complete test suite
- the `authd-edge` PPA

e2e-brokers: google
e2e-tests: allowed_users.robot login_gdm.robot
e2e-ppa: authd-dev

The `e2e-ppa: authd-dev` marker sets the workflow's `authd-ppa` input to
`ubuntu-enterprise-desktop/authd-dev` instead of `authd-edge`.
-->
