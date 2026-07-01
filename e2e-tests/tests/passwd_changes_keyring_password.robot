*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource
Resource        resources/checkpoints.resource

# Test Tags       robot:exit-on-failure

Test Setup    checkpoints.authd User Logged In Via GDM
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${new_password}    passwd1234
${keyring_secret}    s3cr3t-survives-passwd


*** Test Cases ***
Changing the local password also changes the keyring password
    [Documentation]    Changing a remote user's local password with `passwd`
    ...    must re-key the GNOME login keyring so it keeps unlocking with
    ...    the new password.
    ...
    ...    Regression guard: the keyring is unlocked from PAM_AUTHTOK at
    ...    login. If the password change does not propagate the old/new
    ...    password to the keyring's PAM module, the next login creates a
    ...    fresh keyring and any previously stored secret is lost. We seed
    ...    a secret before the change and require it to still be there
    ...    after logging in again with the new password.

    # The gdm-user-registered checkpoint has already logged in as the authd
    # user and verified the keyring is unlocked; seed the secret directly.
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
