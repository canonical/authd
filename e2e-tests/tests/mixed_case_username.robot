*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource
Resource        resources/checkpoints.resource

# Test Tags       robot:exit-on-failure

Test Setup    checkpoints.authd User Created
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Test login with mixed case username
    [Documentation]    Test login with mixed case username via CLI with device
    ...    code flow and local password.

    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}

    # Log in with remote user using mixed case username with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
