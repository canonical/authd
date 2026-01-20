*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    Test Setup
Test Teardown   Test Teardown


*** Keywords ***
Test Setup
    Journal.Start Receiving Journal
    Restore Snapshot    %{BROKER}-edge-configured

Test Teardown
    Journal.Stop Receiving Journal
    Journal.Log Journal
    Log Videos On Error


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Test login with GDM
    [Documentation]    Test login via GDM with device authentication and local password.

    # Log in with remote user with device authentication via GDM
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}

    # Check remote user is properly added to the system
    Open Terminal
    Check If User Was Added Properly    ${username}
    Close Focused Window
    Log Out

    # Log in with remote user with local password via GDM
    Log In With Remote User Through GDM: Local Password    ${username}    ${local_password}
