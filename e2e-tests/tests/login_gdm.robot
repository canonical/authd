*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}             %{BROKER}-installed
${username}             %{E2E_USER}
${user_display_name}    %{E2E_USER_DISPLAY_NAME}
${local_password}       qwer1234


*** Test Cases ***
Test login with GDM
    [Documentation]    Test login via GDM with device code flow and local password.

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

Test switching authentication flow in GDM
    [Documentation]    Verify that selecting the device code flow from GDM's
    ...    Login Options replaces the default local password prompt.

    # Register the user and create the local password used by the default flow.
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Log Out

    # Select the registered user from GDM's login screen. This avoids opening
    # the "Not listed" flow and typing the username again.
    Wait Until GDM Login Screen Ready
    Move Pointer To ${user_display_name}
    Left Button Click
    Match Text    Password    120

    # Select the alternate flow through GDM's Login Options menu. The device
    # URL proves that the new flow replaced the password prompt.
    Select Authentication Mode Through GDM    Device code flow
    Continue Log In With Remote User: Authenticate In External Browser
    # The device code flow asks for a local password before completing login.
    Continue Log In With Remote User Through GDM: Define Local Password    ${local_password}
