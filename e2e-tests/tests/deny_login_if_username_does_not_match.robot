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
Test that login fails if usernames do not match
    [Documentation]    The broker rejects a login when the authenticated name differs
    ...    from the requested name.
    ...
    ...    The test starts as "different_user", then the browser authenticates the
    ...    real test account. Accepting that mismatch could let someone take over the
    ...    local account named in the request.
    ...
    ...    The test logs in a local user first as a baseline.

    # Log in with local user
    Log In

    # Fail to log in if usernames do not match
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code   different_user
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser
    Check That Authenticated User Does Not Match Requested User    different_user
