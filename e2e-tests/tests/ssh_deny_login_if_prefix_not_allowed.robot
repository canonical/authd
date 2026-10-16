*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    Test Setup
Test Teardown   utils.Test Teardown


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}


*** Variables ***
${local_password}    qwer1234


*** Test Cases ***
Test SSH does not offer the broker for an unmatched username
    [Documentation]    The username does not match
    ...    ``ssh_allowed_suffixes_first_auth``, so the broker is not offered over
    ...    SSH.
    ...
    ...    First SSH authentication creates an account. If the broker accepted any
    ...    name, anyone who could reach SSH could start device-code login. It only
    ...    handles names ending in a configured suffix. This test uses the full
    ...    account name as the suffix, not an exact allowlist; longer names ending
    ...    with it also match. ``other-user@<domain>`` does not.
    ...
    ...    authd passes the unmatched name to ``pam_unix``, which shows the plain
    ...    ``Password:`` prompt instead of a provider menu.

    # Log in with local user
    Log In

    # Try SSH login with a username outside the configured suffix.
    ${domain} =    Fetch From Right    %{E2E_USER}    @
    ${username} =    Set Variable    other-user@${domain}
    Open Terminal
    Start Log In With Remote User Through SSH: QR Code    ${username}
    Check That Login Is Handled By PAM Unix
