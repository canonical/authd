*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource
Resource        resources/checkpoints.resource

# Test Tags       robot:exit-on-failure

Test Setup    checkpoints.authd User Created
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}


*** Test Cases ***
Test login with CLI
    [Documentation]    Test login via CLI with device code flow and local
    ...    password.

    # Check remote user is properly added to the system (by checkpoint)
    Check If User Was Added Properly    ${username}
    Check Home Directory    ${username}

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window

    # Try to change username during su login, it should not be possible
    Open Terminal
    Check That Username Cannot Be Changed When Using su    ${username}
    Clear Terminal

    # Check that `su` to a local user goes to the local broker, not authd.
    Check That su To Local User Goes To Local Broker
