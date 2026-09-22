*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login with GDM
    [Documentation]    Test login via GDM with device code flow and local password.

    # Log in with remote user with device code flow via GDM
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Check that GNOME keyring is unlocked

    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Check Home Directory    ${username}
    Check User Shell    ${username}
    Log Out

    # Log in with remote user with local password via GDM
    Log In With Remote User Through GDM: Local Password    ${username}    ${local_password}
    Check that GNOME keyring is unlocked

Test GDM back navigation for a registered user
    [Documentation]    Verify that backing out of the authentication mode screen
    ...    returns to the GDM user list and leaves local password login usable.
    ...    Regression test for https://github.com/canonical/authd/issues/1933

    # Register the user through GDM so this test focuses on GDM navigation.
    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out
    Wait Until GDM Login Screen Ready

    # Select the already-registered user through GDM's username entry.
    # GDM starts the auto-selected local-password flow with its generic
    # "Password" prompt.
    Start Log In With Remote User Through GDM    ${username}
    Match Text    Password    120

    # Escape from the password prompt to authd's authentication-mode
    # selector, where the mode is labeled "Local password".
    Hid.Keys Combo    Escape
    Match Text    Local password    30

    # Escape again from the mode selector must return to GDM's user list,
    # rather than leave GDM in a stale authentication state.
    Hid.Keys Combo    Escape
    Wait Until GDM Login Screen Ready

    # Selecting the user again must show an input field for local-password
    # authentication. The regression rendered a blank page at this point.
    Start Log In With Remote User Through GDM    ${username}
    Match Text    Password    30
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return
    Wait Until Desktop Ready
