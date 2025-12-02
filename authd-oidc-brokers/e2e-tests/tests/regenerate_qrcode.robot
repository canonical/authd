*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

Test Tags       robot:exit-on-failure

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
Test login with CLI and QR code regeneration
    [Documentation]    This test verifies that a remote user can log in using device authentication via CLI with QR code regeneration, and subsequently log in using a local password.

    # Log in with local user
    Log In

    # Log in with remote user with device authentication
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code   ${username}
    Select Provider
    # Let's try regenerating the QR code a couple of times
    Regenerate QR Code
    Regenerate QR Code
    Regenerate QR Code
    # Now we should be able to log in with the remote user using the latest QR code
    Continue Log In With Remote User: Authenticate In External Browser   ${username}
    Continue Log In With Remote User Through CLI: Define Local Password   ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
