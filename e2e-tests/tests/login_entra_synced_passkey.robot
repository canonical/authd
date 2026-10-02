*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Tags         requires:msentraid
Test Setup        Test Setup
Test Teardown     utils.Test Teardown


*** Keywords ***
Test Setup
    ${passkey_user}=    Get Environment Variable    E2E_PASSKEY_USER    ${EMPTY}
    Set Suite Variable    ${username}    ${passkey_user}
    Prepare Synced Passkey Test    E2E_PASSKEY_USER is not set; skipping the synced-passkey Entra test

Passwordless Test Setup
    ${passwordless_passkey_user}=    Get Environment Variable    E2E_PASSWORDLESS_PASSKEY_USER    ${EMPTY}
    Set Suite Variable    ${username}    ${passwordless_passkey_user}
    Prepare Synced Passkey Test    E2E_PASSWORDLESS_PASSKEY_USER is not set; skipping the passwordless synced-passkey Entra test

Prepare Synced Passkey Test
    [Arguments]    ${skip_message}
    utils.Test Setup    snapshot=%{BROKER}-installed
    IF    not $username
        Skip    ${skip_message}
    END
    Change Broker Configuration    register_device    true
    Change Broker Configuration    entra_auth    true
    Change Broker Configuration    device_code    false

Run Passwordless TAP Login Attempt
    VAR    ${tap_id}    ${None}
    VAR    ${login_attempt_started}    ${False}
    VAR    ${attempt_succeeded}    ${False}
    TRY
        ${tap_code}    ${tap_id} =    Wait Until Keyword Succeeds    12x    60s
        ...    EntraTAP.Create TAP For User    ${username}

        ${login_attempt_started} =    Set Variable    ${True}
        Start Log In With Remote User Through GDM    ${username}
        Select Broker Through GDM
        Match Text    Enter your MFA code    120    similarity=88
        Hid.Type String    ${tap_code.value}
        Hid.Keys Combo    Return
        Continue Log In With Remote User Through GDM: Define Local Password    ${local_password}
        ${attempt_succeeded} =    Set Variable    ${True}
    FINALLY
        IF    not $attempt_succeeded and $login_attempt_started
            Run Keyword And Warn On Failure    Log Out
            Run Keyword And Warn On Failure    Wait Until GDM Login Screen Ready
        END
        IF    $tap_id is not None
            Run Keyword And Warn On Failure    Wait Until Keyword Succeeds    3x    2s
            ...    EntraTAP.Delete Tap By Id    ${username}    ${tap_id}
        END
    END


*** Variables ***
${username}    ${EMPTY}
${local_password}    qwer1234





*** Test Cases ***
Test synced passkey user is offered Entra password fallback
    [Documentation]    Verify that a synced-passkey account is offered the Entra ID
    ...    password fallback instead of being left at a local security-key prompt.
    ...
    ...    The test VM has no local security key. Set E2E_PASSKEY_USER to an Entra
    ...    account with a synced passkey before running this local-only test.

    # GDM accepts the full UPN; machinectl's agetty rejects this tenant user's
    # longer-than-32-character login name before PAM reaches authd.
    Start Log In With Remote User Through GDM    ${username}
    Select Broker Through GDM

    # The password fallback must be the first usable step. A pre-fix broker waits
    # for a local security key here and does not reach this prompt promptly.
    Match Text    Enter your Entra ID password    30    similarity=90

Test passwordless synced passkey user falls back to TAP
    [Setup]    Passwordless Test Setup
    [Documentation]    Verify that a synced-passkey account can use a
    ...    passwordless Temporary Access Pass fallback when no local security
    ...    key is available. The TAP helper serializes concurrent release runs.
    ...
    ...    Set E2E_PASSWORDLESS_PASSKEY_USER to an Entra account with a synced
    ...    passkey and passwordless Authenticator sign-in enabled.

    # TAP creation is not an atomic lock. Retry a failed login only while the
    # account is still unprovisioned; logging out the GDM seat cleans up a
    # failed PAM attempt without reverting the VM.
    FOR    ${attempt}    IN RANGE    3
        ${status}    ${message} =    Run Keyword And Ignore Error
        ...    Run Passwordless TAP Login Attempt
        IF    '${status}' == 'PASS'    BREAK
        IF    ${attempt} < 2
            ${user_entry} =    SSH.Execute    getent passwd ${username} || true
            IF    $user_entry
                Log    Not retrying because the remote user is already provisioned.
                BREAK
            END
        END
    END
    Should Be Equal    ${status}    PASS    msg=${message}

    Log Out
