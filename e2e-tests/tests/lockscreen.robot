*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test remote user can unlock the lock screen with a local password
    [Documentation]    Verify that a remote user can unlock their locked desktop
    ...    session with the local password created during device authentication.
    ...
    ...    Screen unlocking runs its own PAM stack, separate from the login one, and
    ...    it runs inside an already-established session. It must accept the cached
    ...    local password rather than sending the user back through the device code
    ...    flow, which they cannot complete from a locked screen.
    ...
    ...    The session is locked with ``loginctl lock-session`` and the test waits for
    ...    LockedHint to confirm the lock took effect before typing the password.

    Log In With Remote User Through GDM: QR Code    ${username}    ${local_password}
    Lock Screen
    Unlock Screen With Password    ${local_password}
    Log Out

