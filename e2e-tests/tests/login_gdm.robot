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


*** Test Cases ***
Test login with GDM
    [Documentation]    Test login via GDM with device code flow and local
    ...    password.

    # The checkpoint has already logged in via QR code; verify the resulting state.
    Check that GNOME keyring is unlocked

    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Check Home Directory    ${username}
    Log Out

    # Log in with remote user with local password via GDM
    Log In With Remote User Through GDM: Local Password
    ...    ${username}    ${local_password}
    Check that GNOME keyring is unlocked
