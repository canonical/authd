*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup          utils.Test Setup    snapshot=%{BROKER}-stable-installed
Test Teardown       utils.Test Teardown


*** Variables ***
${username}             %{E2E_USER}
${local_password}       qwer1234


*** Test Cases ***
Test login with broker version under test
    [Documentation]    A user registered on the stable broker should still log in
    ...    after the broker upgrade.
    ...
    ...    The ``-stable-installed`` snapshot has stable authd and the stable
    ...    broker. A device-code login registers the account and populates the
    ...    broker cache. The test checks setup and a local-password login,
    ...    installs the broker version under test, then checks login, home path,
    ...    and ownership again. authd stays stable to isolate broker changes.

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
