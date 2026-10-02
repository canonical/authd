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
    [Documentation]    The broker denies the remote user when ``owner`` names
    ...    somebody else.
    ...
    ...    With ``allowed_users = OWNER``, the first remote user becomes owner only
    ...    when ``owner`` is unset. Once set, only that user can log in. The test
    ...    sets ``owner`` to another non-empty name so the broker denies this user
    ...    instead of auto-registering them.
    ...
    ...    The identity provider authenticates before the broker denies access. The
    ...    test completes the device-code flow before checking the failure message.
    ...
    ...    The local login is a baseline; it happens before the owner is changed.

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
