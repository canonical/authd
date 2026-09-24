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
Test login after upgrading authd and broker
    [Documentation]    This test verifies that after upgrading authd to the
    ...    version under test and the broker to the version under test, a user
    ...    registered from the stable snapshot can still log in with a local
    ...    password, and their account remains properly set up on the system.

    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window

    Update Broker
    Update Authd

    ${authd_apt_policy}=    SSH.Execute    apt-cache policy authd
    ${gnome_shell_apt_policy}=    SSH.Execute    apt-cache policy gnome-shell yaru-theme-gnome-shell
    Log    authd apt policy:\n${authd_apt_policy}
    Log    gnome-shell apt policy:\n${gnome_shell_apt_policy}

    # Log in with remote user with local password after upgrading
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check Home Directory    ${username}
