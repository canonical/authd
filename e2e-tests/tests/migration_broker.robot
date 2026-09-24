*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource
Resource        resources/checkpoints.resource

# Test Tags       robot:exit-on-failure

Test Setup    checkpoints.authd User Created From Stable Snapshot
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Test login with broker version under test
    [Documentation]    Test local-password login with the broker version under
    ...    test before and after upgrading the broker, using a user registered
    ...    from the stable snapshot.

    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window

    # Install the broker version under test.
    Update Broker

    # Log in with remote user with local password after upgrading
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check Home Directory    ${username}
