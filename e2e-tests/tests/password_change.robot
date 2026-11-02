*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup          utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown       utils.Test Teardown


*** Variables ***
${username}             %{E2E_USER}
${local_password}       qwer1234
${new_password}         passwd1234


*** Test Cases ***
Test changing local password of remote user
    [Documentation]    A remote user can change the local password with ``passwd``
    ...    and log in with the new one.
    ...
    ...    The broker stores and verifies the local-password hash. Users can use it
    ...    when the provider is unreachable. ``passwd`` must go through authd to the
    ...    broker, not ``pam_unix``, to keep the stored credential current.
    ...
    ...    Device-code login sets the first local password. The test changes it with
    ...    ``passwd`` in an ``su`` session, then logs in again with the new password.
    ...    passwd_changes_keyring_password.robot covers the keyring update.
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
