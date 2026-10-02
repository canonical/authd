*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${new_password}    passwd1234


*** Test Cases ***
Test changing local password of remote user
    [Documentation]    Verify that a remote user can change their local password with
    ...    ``passwd`` and then log in with the new one.
    ...
    ...    The local password is the credential authd caches during device
    ...    authentication and the only one that works while the provider is
    ...    unreachable, so ``passwd`` has to be routed through authd rather than
    ...    pam_unix for a managed user.
    ...
    ...    Steps:
    ...      1. register the user with the device code flow, which sets the first local
    ...         password
    ...      2. log in with ``su`` and change the password with ``passwd``
    ...      3. log in again with ``su`` using the new password
    ...
    ...    passwd_changes_keyring_password.robot additionally covers that this change
    ...    re-keys the GNOME login keyring.

    # Log in with local user
    Log In

    # Log in with remote user with device code flow
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    # Change local password of remote user
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Change Password    ${local_password}    ${new_password}
    Log Out From su Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${new_password}
