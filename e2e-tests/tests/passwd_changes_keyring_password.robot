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
${new_password}    passwd1234
${keyring_secret}    s3cr3t-survives-passwd


*** Test Cases ***
Changing the local password also changes the keyring password
    [Documentation]    Changing a remote user's local password with ``passwd`` also
    ...    updates the GNOME login keyring.
    ...
    ...    The keyring unlocks from ``PAM_AUTHTOK`` at login. If the password change
    ...    fails to pass both old and new passwords to the keyring's PAM module,
    ...    the next login creates a fresh keyring and loses stored secrets. The test
    ...    stores a secret before the change and checks it after a login with the
    ...    new password.

    # Log in with device authentication. This creates the login keyring and
    # unlocks it with ${local_password}.
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Check that GNOME keyring is unlocked
    Store Secret In GNOME Keyring    ${keyring_secret}

    # Change the local password with `passwd`.
    Open Terminal
    Change Password    ${local_password}    ${new_password}
    Close Focused Window
    Log Out

    # Log in again with the new local password. The keyring must unlock with it
    # and still contain the secret stored before the change.
    Log In With Remote User Through GDM: Local Password    ${username}    ${new_password}
    Check that GNOME keyring is unlocked
    Check Secret In GNOME Keyring    ${keyring_secret}
