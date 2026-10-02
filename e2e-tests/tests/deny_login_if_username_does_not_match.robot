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
    [Documentation]    Verify that a login started for one user but authenticated as
    ...    another is rejected.
    ...
    ...    The login is started for "different_user" while the browser step
    ...    authenticates the real test account, so the requested name and the
    ...    authenticated identity disagree. The broker must notice that and fail the
    ...    login; accepting it would let anyone claim an arbitrary account name and
    ...    take over the matching local user.
    ...
    ...    The local user logs in first to confirm local authentication is unaffected.

    # Log in with local user
    Log In

    # Fail to log in if usernames do not match
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code   different_user
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser
    Check That Authenticated User Does Not Match Requested User    different_user
