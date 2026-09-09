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


Test first GDM login enforces password quality
    [Documentation]    Verify that weak local passwords are rejected while creating
    ...    the password used after the first device-authenticated GDM login.

    ${short_password} =    Set Variable    1234
    ${dictionary_password} =    Set Variable    password
    ${good_password} =    Set Variable    2FuA2M3jfGl

    Start Log In With Remote User Through GDM    ${username}
    Select Broker Through GDM
    Continue Log In With Remote User: Authenticate In External Browser
    Continue Log In With Remote User Through GDM: Define Local Password With Quality Checks
    ...    ${short_password}    ${dictionary_password}    ${good_password}

    Check If User Was Added Properly    ${username}    ${good_password}
    Check Home Directory    ${username}
    Check User Shell    ${username}
    Log Out
