*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${new_password}    Passwd1234


*** Test Cases ***
Test changing local password of local user
    [Documentation]    A local user with an expired password can complete the forced
    ...    password change through GDM while authd is installed.
    ...
    ...    PAM calls authd at login, but unmanaged local users rely on pam_unix's
    ...    password-expiry flow. If it breaks, local administrators can be locked
    ...    out when their passwords expire.
    ...
    ...    Steps:
    ...    - log in as the local user and expire the password with ``passwd -e``
    ...    - log out
    ...    - log in again, complete the forced change, and reach the desktop
    ...
    ...    local_user_passwd_change_cli.robot covers voluntary ``passwd`` changes
    ...    and password-quality checks, not password expiry.

    # Log in with local user
    Log In

    # Change password for local user
    Open Terminal In Sudo Mode
    Force Password Change
    Close Terminal In Sudo Mode
    Log Out

    # Log in with new password
    Log In And Set Password    ${new_password}
