*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    Test Setup
Test Teardown   Test Teardown


*** Keywords ***
Test Setup
    Restore Snapshot    %{BROKER}-edge-configured
    Journal.Start Receiving Journal
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}

Test Teardown
    Journal.Stop Receiving Journal
    Journal.Log Journal
    Log Videos On Error


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Test login with SSH
    [Documentation]    Test login via SSH with device authentication and local password.

    # Log in with local user
    Log In

    # Log in with remote user with device authentication through SSH
    Open Terminal
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From SSH Session
    Close Focused Window

    # Log in with remote user with local password through SSH
    Open Terminal
    Log In With Remote User Through SSH: Local Password    ${username}    ${local_password}
