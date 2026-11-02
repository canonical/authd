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
Test polkit authentication as authd user via pkexec after initial GDM login
    [Documentation]    ``pkexec`` asks Polkit to authenticate the authd user with
    ...    the local password.
    ...
    ...    The GNOME Polkit agent displays the PAM conversation. authd sends the
    ...    prompts through PAM. This is a different caller from GDM, ``su`` and
    ...    ``sudo``.
    ...
    ...    The test adds the authd user to the sudo group so Polkit asks for that
    ...    user's credentials instead of the local admin (ubuntu).
    ...
    ...    It runs ``pkexec touch`` and checks over SSH that the marker exists and
    ...    is owned by root.

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
    Wait Until Keyword Succeeds    20s    1s
    ...    Check Marker File Is Root Owned    /tmp/polkit-authd-test

    Log Out

Test polkit authentication as authd user via pkexec using Entra ID password and MFA after initial GDM login
    [Documentation]    A user can authenticate with the Entra ID password and MFA
    ...    through Polkit after the initial GDM login.
    ...
    ...    The broker lists local-password mode first, so authd selects it.
    ...    The test cancels that prompt, returns to the flow menu, and selects
    ...    Entra ID. This covers switching from local-password mode to another
    ...    flow through the Polkit PAM conversation.
    ...
    ...    The Entra test account already belongs to sudo, so Polkit can ask for its
    ...    credentials without the ``usermod`` step used above.
    [Tags]    requires:msentraid
    [Setup]    Entra Polkit Test Setup

    Log In With Remote User Through GDM: Entra Password    ${username}
    Check If User Was Added Properly    ${username}    %{E2E_PASSWORD}

    SSH.Execute    rm -f /tmp/polkit-authd-test-entra

    Open Terminal

    # Run pkexec to create the marker file as root; this triggers the polkit agent.
    Hid.Type String    pkexec touch /tmp/polkit-authd-test-entra
    Hid.Keys Combo    Return

    Log In With Remote User Through Polkit: Entra Password    ${username}    /tmp/polkit-authd-test-entra

    # Verify polkit granted access: the marker file must exist and be root-owned.
    Wait Until Keyword Succeeds    30s    5s
    ...    Check Marker File Is Root Owned    /tmp/polkit-authd-test-entra
    Log Out


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
