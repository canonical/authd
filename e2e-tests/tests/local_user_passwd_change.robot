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
    [Documentation]    Verify that a local user whose password has expired can still
    ...    complete the forced password change through GDM with authd installed.
    ...
    ...    authd inserts itself into the PAM stack for every login, so the ordinary
    ...    pam_unix password-expiry path has to keep working for users that authd does
    ...    not manage. Breaking it would lock local administrators out of the machine
    ...    the first time their password expires.
    ...
    ...    Steps:
    ...      1. log in as the local user and expire the password with ``passwd -e``
    ...      2. log out
    ...      3. log in again, complete the forced change, and reach the desktop
    ...
    ...    local_user_passwd_change_cli.robot covers the same path through ``passwd``
    ...    in a terminal.

    # Log in with local user
    Log In

    # Change password for local user
    Open Terminal In Sudo Mode
    Force Password Change
    Close Terminal In Sudo Mode
    Log Out

    # Log in with new password
    Log In And Set Password    ${new_password}
