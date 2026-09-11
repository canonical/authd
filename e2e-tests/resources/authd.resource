*** Settings ***
Documentation       Authd related test resources

Resource            kvm.resource
Resource            ./utils.resource


*** Variables ***
${local_password}    qwer1234
${AUTHD_CFG}    /etc/authd/authd.yaml


*** Keywords ***
Enable Edge Repository For Authd
    SSH.Execute    add-apt-repository ppa:ubuntu-enterprise-desktop/authd-edge -y  timeout=300


Change Authd Configuration
    [Documentation]    Sets a top-level key in the authd configuration file and restarts authd so
    ...                that the new value takes effect. The key is uncommented if it was not set
    ...                yet, and the result is verified, because a typo in the key would otherwise
    ...                make the substitution a silent no-op.
    [Arguments]    ${config_key}    ${config_value}
    SSH.Execute    sed -i 's|^#*${config_key}:.*|${config_key}: ${config_value}|' ${AUTHD_CFG}
    Check Authd Configuration Value    ${config_key}    ${config_value}
    SSH.Execute    systemctl restart authd.service


Check Authd Configuration Value
    [Arguments]    ${config_key}    ${expected_value}
    ${content} =    SSH.Execute    grep '^${config_key}:' ${AUTHD_CFG} || true
    Should Be Equal    ${content}    ${config_key}: ${expected_value}


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


Check That Authenticated User Does Not Match Requested User
    [Arguments]    ${requested_user}
    Match Text    Authentication failure: requested username "${requested_user}" does not match the authenticated username    120


Check That Remote User Can Run Sudo Commands
    [Arguments]    ${local_password}
    SSH.Execute    echo ${local_password} | sudo -S touch sudo-test.txt
    ${output} =    SSH.Execute    ls -l sudo-test.txt
    Should Contain    ${output}    sudo-test.txt


Check User Is Resolved By Both Names
    [Documentation]    Asserts that NSS resolves the shortened and the fully qualified username to
    ...                the very same entry, and that the entry is named after the shortened form.
    ...                This is what lets the rest of the system keep working with either name once
    ...                authd stores the user under a shortened one.
    [Arguments]    ${short_username}    ${full_username}
    ${by_short_name} =    SSH.Execute    getent passwd ${short_username}
    ${by_full_name} =    SSH.Execute    getent passwd ${full_username}
    Should Be Equal    ${by_short_name}    ${by_full_name}
    Should Start With    ${by_short_name}    ${short_username}:x:


Check User Private Group Follows The Short Username
    [Documentation]    Asserts that the user private group is named after the shortened username
    ...                and shares the user UID, as authd creates it with the same ID. authd leaves
    ...                the group password field empty, so the entry reads "name::gid:members".
    [Arguments]    ${short_username}
    ${uid} =    SSH.Execute    id -u ${short_username}
    ${group} =    SSH.Execute    getent group ${short_username}
    Should Start With    ${group}    ${short_username}::${uid}:


Check User Is Resolved By The Full Username Only
    [Documentation]    Asserts that the user is stored under their fully qualified username and
    ...                that the shortened name does not resolve, which is the state expected once
    ...                short usernames have been disabled and the user has logged in again.
    [Arguments]    ${short_username}    ${full_username}
    ${by_full_name} =    SSH.Execute    getent passwd ${full_username}
    Should Start With    ${by_full_name}    ${full_username}:x:
    ${by_short_name} =    SSH.Execute    getent passwd ${short_username} || echo NOT-FOUND
    Should Be Equal    ${by_short_name}    NOT-FOUND


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
