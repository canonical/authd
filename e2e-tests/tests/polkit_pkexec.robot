*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Keywords ***
Entra Polkit Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Change Broker Configuration    register_device    true
    Change Broker Configuration    entra_auth    true
    Change Broker Configuration    device_code    false


Check Marker File Is Root Owned
    [Arguments]    ${marker_file}
    ${owner} =    SSH.Execute    stat -c %u ${marker_file}
    Should Be Equal As Integers    ${owner}    0


*** Test Cases ***
Test polkit authentication as authd user via pkexec after initial GDM login
    [Documentation]    Verify that pkexec authenticates the authd user through
    ...    polkit using their local password.
    ...
    ...    The authd user is also added to the sudo group so that polkit prompts
    ...    for their own credentials rather than falling back to the local admin
    ...    (ubuntu) user.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}

    # Add the authd user to the sudo group so that polkit authenticates them as
    # themselves rather than falling back to the local admin (ubuntu) user.
    SSH.Execute    sudo usermod -aG sudo ${username}

    SSH.Execute    rm -f /tmp/polkit-authd-test
    Open Terminal

    # Run pkexec to create the marker file as root; this triggers the polkit agent.
    Hid.Type String    pkexec touch /tmp/polkit-authd-test
    Hid.Keys Combo    Return

    # The GNOME polkit agent pops up a dialog; since the authd user is in the sudo
    # group, polkit authenticates them as themselves through PAM/authd, which shows
    # the authd local-password prompt inside the polkit dialog.
    Match Text    Enter your password    60
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    # Verify polkit granted access: the marker file must exist and be root-owned.
    Wait Until Keyword Succeeds    5s    1s
    ...    Check Marker File Is Root Owned    /tmp/polkit-authd-test

    Log Out

Test polkit authentication as authd user via pkexec using Entra ID password and MFA after initial GDM login
    [Tags]    requires:msentraid
    [Setup]    Entra Polkit Test Setup
    [Documentation]    Verify that pkexec authenticates the authd user through
    ...    polkit using their Entra ID password + MFA flow.
    ...
    ...    The authd user is also added to the sudo group so that polkit prompts
    ...    for their own credentials rather than falling back to the local admin
    ...    (ubuntu) user.

    Log In With Remote User Through GDM: Entra Password    ${username}

    SSH.Execute    rm -f /tmp/polkit-authd-test-entra

    Open Terminal

    # Run pkexec to create the marker file as root; this triggers the polkit agent.
    Hid.Type String    pkexec touch /tmp/polkit-authd-test-entra
    Hid.Keys Combo    Return

    Log In With Remote User Through Polkit: Entra Password    ${username}    

    # Verify polkit granted access: the marker file must exist and be root-owned.
    Wait Until Keyword Succeeds    30s    5s
    ...    Check Marker File Is Root Owned    /tmp/polkit-authd-test-entra
    Log Out
