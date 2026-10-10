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
Test login with GDM
    [Documentation]    Verify the core GDM login paths for a remote user: the first
    ...    login with the device code flow and later logins with the cached local
    ...    password.
    ...
    ...    Steps:
    ...      1. log in with the device code flow, set a local password, and check the
    ...         GNOME login keyring is unlocked
    ...      2. check the user is visible through NSS, has a home directory owned by
    ...         them, and, for MS Entra ID, is in the expected groups and can run sudo;
    ...         also check /usr/bin/bash as their shell
    ...      3. log out, log in again with the local password, and check the keyring is
    ...         unlocked by that path too
    ...
    ...    The keyring checks matter because it is unlocked from PAM_AUTHTOK during
    ...    login. If authd does not pass the password on to the keyring PAM module,
    ...    the session starts with a locked keyring and applications prompt for it.

    # Log in with remote user with device code flow via GDM
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Check that GNOME keyring is unlocked

    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Check Home Directory    ${username}
    Check User Shell    ${username}
    Log Out

    # Log in with remote user with local password via GDM
    Log In With Remote User Through GDM: Local Password    ${username}    ${local_password}
    Check that GNOME keyring is unlocked
