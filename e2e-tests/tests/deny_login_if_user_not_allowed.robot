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


*** Test Cases ***
Test that changing owner prevents remote logins
    [Documentation]    Verify that a remote user is denied when the broker owner is set
    ...    to somebody else.
    ...
    ...    With the default ``allowed_users = OWNER`` policy only the registered owner
    ...    may log in. Setting ``owner`` to a different, non-empty user name makes the
    ...    broker compare names and deny access. An empty owner would instead trigger
    ...    auto-registration, which config_owner_auto_update.robot covers.
    ...
    ...    The denial happens after the browser step, not before it: the identity
    ...    provider authenticates the user successfully and the broker rejects them
    ...    afterwards. The test therefore drives the whole device code flow before
    ...    asserting on the failure message.
    ...
    ...    The local user logs in first to confirm local authentication is unaffected.

    # Log in with local user
    Log In

    # Change owner to another user
    Change Broker Configuration    owner    different-user

    # Log in with remote user with device code flow
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser
    Check That Remote User Is Not Allowed To Log In
