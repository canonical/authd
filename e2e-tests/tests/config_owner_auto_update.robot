*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test that owner is auto-updated in broker configuration
    [Documentation]    Verify that the first remote user to log in is registered as the
    ...    broker owner.
    ...
    ...    The broker ships with ``allowed_users = OWNER`` and no owner set. To make
    ...    that usable without manual configuration, it registers the first user who
    ...    logs in by writing an ``owner`` entry into a
    ...    ``20-owner-autoregistration.conf`` drop-in. Every later user is then denied
    ...    until an administrator widens allowed_users.
    ...
    ...    Owner registration happens during the login, so the check polls for the
    ...    drop-in instead of waiting a fixed amount of time.

    # Log in with local user
    Log In

    # Try to log in with remote user
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    # Check that owner was updated in broker configuration
    Wait Until Keyword Succeeds    30s    1s    Check If Owner Was Registered    ${username}
