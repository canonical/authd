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
Test login after upgrading authd and broker
    [Documentation]    Verify that a user registered on the stable authd and stable
    ...    broker can still log in after both are upgraded to the versions under test.
    ...
    ...    This is the full upgrade path that users actually go through. The user
    ...    database, the broker configuration and the cached token are all created by
    ...    the released versions, and neither upgrade may invalidate them.
    ...
    ...    Steps:
    ...      1. register the user with the device code flow on the stable stack and
    ...         check the account is set up correctly
    ...      2. log in again with the local password
    ...      3. upgrade the broker snap, then authd, and gnome-shell with it because
    ...         authd constrains which gnome-shell versions it works with
    ...      4. log in again with the local password and check the home directory
    ...         survived the upgrade
    ...
    ...    migration_authd.robot and migration_broker.robot upgrade only one component
    ...    each, which narrows down which upgrade broke things when this test fails.

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

    Update Broker
    Update Authd    skip_if_authd_stable_ppa_is_unavailable=${True}

    ${authd_apt_policy}=    SSH.Execute    apt-cache policy authd
    ${gnome_shell_apt_policy}=    SSH.Execute    apt-cache policy gnome-shell yaru-theme-gnome-shell
    Log    authd apt policy:\n${authd_apt_policy}
    Log    gnome-shell apt policy:\n${gnome_shell_apt_policy}

    # Log in with remote user with local password after upgrading
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check Home Directory    ${username}
