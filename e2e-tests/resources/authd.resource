*** Settings ***
Documentation       Authd related test resources

Resource            kvm.resource
Resource            ./utils.resource


*** Variables ***
${local_password}    qwer1234


*** Keywords ***
Disable Authd Socket And Service
    SSH.Execute    systemctl mask --now authd.socket
    SSH.Execute    systemctl mask --now authd.service


Change Password
    [Arguments]    ${local_password}    ${new_password}
    Hid.Type String    passwd
    Hid.Keys Combo    Return

    Match Text    regex:Enter\\s*your\\s*(?:local\\s*)?password:    15
    Builtin.Sleep    2
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Match Text    New password:    15
    Builtin.Sleep    2
    Hid.Type String    ${new_password}
    Hid.Keys Combo    Return

    Match Text    Confirm password:    15
    Builtin.Sleep    2
    Hid.Type String    ${new_password}
    Hid.Keys Combo    Return

    Match Text    passwd: password updated successfully    15


Try Log In With Remote User
    [Arguments]    ${username}
    Try machinectl login Prompt
    Hid.Type String    ${username}
    Hid.Keys Combo    Return


# Conditional checks
Check That Remote User Has No Available Authentication Modes
    Match Text    Error connecting to provider. Check your network connection.    120


Check That Log In Fails Because Authd Is Disabled
    Match Text    could not connect to unix:///run/authd.sock    120


Check That User Is Redirected To Local Broker
    Match Text    Password:    120


Get Journal Cursor
    ${journal_cursor} =    SSH.Execute
    ...    sudo journalctl --no-pager -n 1 --show-cursor | sed -n 's/^-- cursor: //p'
    Should Not Be Empty    ${journal_cursor}
    RETURN    ${journal_cursor}


Check That Authenticated User Does Not Match Requested User
    [Arguments]    ${requested_user}    ${journal_cursor}    ${console_log}
    Wait Until Keyword Succeeds    30s    2s
    ...    Check Journal For Username Mismatch Failure    ${requested_user}    ${journal_cursor}
    Wait Until Keyword Succeeds    30s    2s
    ...    Check Console For Username Mismatch Failure    ${requested_user}    ${console_log}


Check Journal For Username Mismatch Failure
    [Arguments]    ${requested_user}    ${journal_cursor}
    ${log_line} =    SSH.Execute
    ...    sudo journalctl --no-pager --after-cursor="${journal_cursor}" | grep -E 'Authentication failure: requested username "${requested_user}" does not match the authenticated (user|username) "' || true
    Should Not Be Empty    ${log_line}
    ...    No journal entry found for a username-mismatch authentication failure with requested user "${requested_user}"


Check Console For Username Mismatch Failure
    [Arguments]    ${requested_user}    ${console_log}
    ${console_line} =    SSH.Execute
    ...    grep -E -- 'Authentication failure: requested username "${requested_user}" does not match the authenticated (user|username) "' "${console_log}" || true
    Should Not Be Empty    ${console_line}
    ...    No username-mismatch authentication failure was sent to the terminal console


Check That Remote User Can Run Sudo Commands
    [Arguments]    ${local_password}
    SSH.Execute    echo ${local_password} | sudo -S touch sudo-test.txt
    ${output} =    SSH.Execute    ls -l sudo-test.txt
    Should Contain    ${output}    sudo-test.txt


Check that GNOME keyring is unlocked
    SSH.Execute As Current User    echo '' | secret-tool store --label=test test key


Store Secret In GNOME Keyring
    [Arguments]    ${secret}
    # Storing succeeds only if the keyring is unlocked, so this both seeds a
    # known secret and asserts the keyring is currently unlocked.
    SSH.Execute As Current User    printf '%s' '${secret}' | secret-tool store --label=e2e-test e2e-test keyring-persistence


Check Secret In GNOME Keyring
    [Arguments]    ${expected_secret}
    # Looking up the secret succeeds only if the *same* keyring that stored it is
    # unlocked. If the password change re-created the keyring instead of re-keying
    # it, the secret is gone and this fails.
    ${secret} =    SSH.Execute As Current User    secret-tool lookup e2e-test keyring-persistence
    Should Be Equal    ${secret}    ${expected_secret}
