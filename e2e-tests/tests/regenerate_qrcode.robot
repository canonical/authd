*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup          utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown       utils.Test Teardown


*** Variables ***
${username}             %{E2E_USER}
${local_password}       qwer1234


*** Test Cases ***
Test login with CLI and QR code regeneration
    [Documentation]    The test starts a device-code login, requests three
    ...    replacement codes, and signs in with the last one.
    ...
    ...    Each request should change the displayed code. The test checks each
    ...    replacement, then sets a local password, checks setup, and logs in
    ...    again through ``su``.

    # Log in with local user
    Log In

    # Log in with remote user with device code flow
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    ${user_code} =    Wait Until Keyword Succeeds    30s    1s    Extract Device Code
    FOR    ${attempt}    IN RANGE    3
        Regenerate QR Code
        ${user_code} =    Wait Until Keyword Succeeds    30s    1s
        ...    Get New Device Code    ${user_code}
    END
    Continue Log In With Remote User: Authenticate In External Browser
    Continue Log In With Remote User Through CLI: Define Local Password    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}


*** Keywords ***
Get New Device Code
    [Arguments]    ${previous_code}
    ${new_code} =    Extract Device Code
    Should Not Be Equal    ${new_code}    ${previous_code}
    RETURN    ${new_code}
