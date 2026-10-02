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
Test login with broker version under test
    [Documentation]    Verify that a user registered on the stable broker can still log
    ...    in after the broker is upgraded to the version under test.
    ...
    ...    The test starts from the ``-stable-installed`` snapshot, so the broker
    ...    configuration and the cached token are written by the released broker snap.
    ...    Refreshing the snap must not invalidate them or require the user to go
    ...    through the device code flow again.
    ...
    ...    Steps:
    ...      1. register the user with the device code flow on the stable broker and
    ...         check the account is set up correctly
    ...      2. log in again with the local password
    ...      3. install the broker version under test
    ...      4. log in again with the local password and check the home directory
    ...         survived the upgrade
    ...
    ...    Only the broker is upgraded here; authd stays on its stable version, which
    ...    tells an upgraded broker apart from an upgraded authd when this fails.

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
