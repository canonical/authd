*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-stable-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login after upgrading authd and broker to edge channel
    [Documentation]    This test verifies that after upgrading both authd and the broker to the edge channel, remote users can still log in using device code flow and local password, and their accounts are properly set up on the system.

    # Log in with local user
    Log In

    # Log in with remote user with device code flow
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Terminal In Sudo Mode

    # Disable entra_password before upgrading: the edge broker refuses to start
    # if this flow is enabled without register_device or a client_secret, which
    # are both not configured.
    Disable Entra Password Via Drop In

    # Switch to the edge channel for the broker snap and the edge PPA for authd
    Enable Edge Repository For Authd
    Enable Edge Broker
    Update Authd

    # Log in with remote user with local password after upgrading
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check Home Directory    ${username}
