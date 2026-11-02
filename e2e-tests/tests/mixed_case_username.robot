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
Test login with mixed-case username
    [Documentation]    A mixed-case username still logs in and resolves
    ...    through NSS.
    ...
    ...    authd stores usernames in lowercase. The test title-cases
    ...    ``E2E_USER`` before both login flows and checks that NSS returns
    ...    the lowercase record.
    # Convert To Title Case skips words that are not all lower case.
    ${username} =    String.Convert To Lower Case    ${username}
    ${username} =    String.Convert To Title Case    ${username}

    # Log in with local user
    Log In

    # Log in with remote user using mixed case username with device code flow
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user using mixed case username with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
