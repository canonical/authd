*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login with CLI and QR code regeneration
    [Documentation]    Verify that a remote user can complete the device code flow
    ...    after asking for a new code several times.
    ...
    ...    Device codes expire, so the flow lets the user request a fresh one without
    ...    restarting the login. Each request has to replace the code on screen and
    ...    start a new device authorization; authenticating with the latest code must
    ...    still complete the login.
    ...
    ...    Steps:
    ...      1. start the device code flow and request a new code three times
    ...      2. authenticate in the browser with the last code and set a local password
    ...      3. check the user is set up correctly on the system
    ...      4. log in again with ``su`` using the local password

    # Log in with local user
    Log In

    # Log in with remote user with device code flow
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code   ${username}
    Select Provider
    # Let's try regenerating the QR code a couple of times
    Regenerate QR Code
    Regenerate QR Code
    Regenerate QR Code
    # Now we should be able to log in with the remote user using the latest QR code
    Continue Log In With Remote User: Authenticate In External Browser
    Continue Log In With Remote User Through CLI: Define Local Password   ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
