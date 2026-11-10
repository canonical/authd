*** Settings ***
Resource         resources/utils.resource
Resource         resources/authd.resource
Resource         resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup       Test Setup
Test Teardown    utils.Test Teardown


*** Variables ***
${username}          %{E2E_USER}
${local_password}    qwer1234
${unix_username}     authd-e2e-unix@example.com
${session_file}      /tmp/authd-ssh-provider-username


*** Test Cases ***
Test login with SSH
    [Documentation]    The first SSH login uses device code. Later logins use the
    ...    cached local password.
    ...
    ...    On first login, the broker handles names matching
    ...    ``ssh_allowed_suffixes_first_auth``. The test allows the E2E account.
    ...
    ...    The first login uses ``ssh <user>@localhost`` for device-code flow, sets
    ...    a local password, and checks account setup. A later SSH login uses that
    ...    password.
    ...
    ...    SSH uses keyboard-interactive PAM and prompts with "Choose your provider:",
    ...    "Choose action:", and "Create a local password:". This differs from the
    ...    CLI terminal UI, so SSH needs separate coverage.

    # Log in with local user
    Log In

    # Log in with remote user with device code flow through SSH
    Open Terminal
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From SSH Session
    Close Focused Window

    # Log in with remote user with local password through SSH
    Open Terminal
    Log In With Remote User Through SSH: Local Password    ${username}    ${local_password}

Test SSH provider username keeps the Unix identity
    [Documentation]    SSH keeps the stable Unix identity when the provider name
    ...    differs, using the real NSS module and OpenSSH server.
    ...
    ...    The preloaded PAM integration tests give every fake user the same UID.
    ...    They cannot check Ubuntu sshd's UID check when PAM changes ``PAM_USER``.
    ...    This test uses the installed OpenSSH server and NSS module on each
    ...    release. It logs the package versions because OpenSSH updates can
    ...    change whether a username change is accepted.
    ...
    ...    Register the real IdP account, then change only its stored Unix name
    ...    while authd is stopped. This models a provider username change without
    ...    changing the shared IdP account. The provider ID, UID and home stay
    ...    unchanged. Both names must resolve to the same NSS entry.
    ...
    ...    Ubuntu 24.04 must deny provider aliases, including retries on the same
    ...    connection. A fresh login with the Unix name must still work. Newer
    ...    releases accept the provider alias. Successful sessions must use the
    ...    stored Unix name, UID and home, and create files owned by that UID.
    ...    The fixture Unix name matches the VM's email-style SSH user rule.
    ...    ``allowed_users = ALL`` isolates these checks from owner-name policy.

    Prepare SSH User With A Distinct Unix Username

    # Check that both names and the UID resolve to the same NSS record.
    ${provider_entry}=    SSH.Execute    getent passwd ${username}
    ${unix_entry}=    SSH.Execute    getent passwd ${unix_username}
    Should Be Equal    ${provider_entry}    ${unix_entry}
    Should Start With    ${unix_entry}    ${unix_username}:x:${uid}:
    ${nss_entry}=    SSH.Execute    getent -s authd passwd ${username}
    Should Be Equal    ${nss_entry}    ${unix_entry}
    ${reverse_entry}=    SSH.Execute    getent passwd ${uid}
    Should Be Equal    ${reverse_entry}    ${unix_entry}

    SSH.Execute    test ! -e ${session_file}
    ${release}=    SSH.Execute    . /etc/os-release && printf '%s' "$VERSION_ID"
    IF    $release == '24.04'
        Check SSH Provider Alias Is Denied
        ${login_name}=    Set Variable    ${unix_username}
    ELSE
        ${login_name}=    Set Variable    ${username}
    END
    Open Terminal
    Log In With Remote User Through SSH: Local Password
    ...    ${login_name}    ${local_password}    expected_username=${unix_username}

    # Check the session identity and file owner.
    Hid.Type String    umask 077; { id -un; id -u; printf '%s\\n' "$HOME"; } > ${session_file}
    Hid.Keys Combo    Return
    ${expected}=    Catenate    SEPARATOR=\n    ${unix_username}    ${uid}    ${home}
    Wait Until Keyword Succeeds    30s    1s    Check SSH Session Identity    ${expected}
    Log Out From SSH Session

Test SSH stale connections cannot change the Unix identity
    [Documentation]    Overlapping SSH connections must not use a temporary UID
    ...    after authentication selects an existing Unix account.
    ...
    ...    Register a real IdP account and give it a distinct Unix name. Replace
    ...    its stored alias with an old one to model a provider rename that authd
    ...    has not learned yet. Two real SSH clients reach their password prompts
    ...    before either authenticates, so both retain the same temporary NSS
    ...    identity. Completing the first login must not let the second use that
    ...    stale UID, including a retry on the second connection.
    ...
    ...    Then restart authd while a correctly authenticated Unix-name session
    ...    is open. Check the live session's UID, GID, home, groups and file owner
    ...    after the restart. A new connection after the restart must follow the
    ...    same rules, and a fresh Unix-name login must still work. Fresh
    ...    known-alias logins follow the release policy: denied on 24.04 and
    ...    allowed on newer releases.
    ...
    ...    The helper uses sockets to hold password prompts, not fixed sleeps.
    ...    All identities come from the real NSS module, PAM and OpenSSH server.
    ...    ``allowed_users = ALL`` isolates these checks from owner-name policy.
    ...    First-time Unix accounts need separate restart/UID-lifetime coverage.

    Prepare SSH User With A Distinct Unix Username
    ${result}=    Run SSH Pre-Auth Helper    run '${username}' '${unix_username}' '${local_password}'
    Log    ${result}


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}

Prepare SSH User With A Distinct Unix Username
    ${versions}=    SSH.Execute    dpkg-query -W authd openssh-server
    Log    ${versions}
    Log In
    Open Terminal
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From SSH Session
    Close Focused Window
    ${uid}=    SSH.Execute    id -u ${username}
    Set Test Variable    ${uid}
    ${home}=    SSH.Execute    getent passwd ${username} | cut -d: -f6
    Should Not Be Empty    ${home}
    Set Test Variable    ${home}
    SSH.Execute    loginctl terminate-user ${username} || test -z "$(pgrep -u ${uid})"
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute    test -z "$(pgrep -u ${uid})"
    Change allowed_users In Broker Configuration    ALL
    Give Account A Distinct Unix Username

Give Account A Distinct Unix Username
    [Documentation]    Model an account whose provider name changed without
    ...    renaming the real IdP account. Keep its UID, home and provider binding.
    Run SSH Pre-Auth Helper    rename '${username}' '${unix_username}'

Run SSH Pre-Auth Helper
    [Documentation]    Run a command of ``ssh_pre_auth.py`` in the VM and return its output.
    [Arguments]    ${arguments}
    ${driver_dir}=    SSH.Execute    mktemp -d
    TRY
        SSH.Copy To VM    ${CURDIR}/../resources/ssh_pre_auth.py    ${driver_dir}/ssh_pre_auth.py
        ${result}=    SSH.Execute    python3 ${driver_dir}/ssh_pre_auth.py ${arguments}    timeout=300
        RETURN    ${result}
    FINALLY
        SSH.Execute    rm -f ${driver_dir}/ssh_pre_auth.py && rmdir ${driver_dir}
    END

Check SSH Session Identity
    [Arguments]    ${expected}
    ${actual}=    SSH.Execute    cat ${session_file}
    Should Be Equal    ${actual}    ${expected}
    ${owner}=    SSH.Execute    stat -c %u ${session_file}
    Should Be Equal    ${owner}    ${uid}

Check SSH Provider Alias Is Denied
    ${auth_dir}=    SSH.Execute    mktemp -d
    TRY
        SSH.Execute
        ...    printf '%s\\n' '#!/bin/sh' 'printf "%s\\n" "${local_password}"' > ${auth_dir}/askpass && chmod 700 ${auth_dir}/askpass
        FOR    ${attempts}    IN    1    2
            ${cursor}=    SSH.Execute
            ...    journalctl -u authd.service -n 0 --show-cursor | sed -n 's/^-- cursor: //p'
            Should Not Be Empty    ${cursor}
            ${stdout}    ${stderr}    ${status}=    SSH.Execute With Status
            ...    env SSH_ASKPASS=${auth_dir}/askpass SSH_ASKPASS_REQUIRE=force DISPLAY=:0 setsid ssh -o StrictHostKeyChecking=accept-new -o PreferredAuthentications=keyboard-interactive -o NumberOfPasswordPrompts=${attempts} '${username}@localhost' 'exit 99' </dev/null
            ...    timeout=120
            Should Be Equal As Integers    ${status}    255
            ${journal}=    SSH.Execute
            ...    journalctl --sync && journalctl -u authd.service --after-cursor='${cursor}' -o cat --no-pager
            ${granted}=    Get Count    ${journal}    Authentication result: granted
            Should Be Equal As Integers    ${granted}    ${attempts}
        END
    FINALLY
        SSH.Execute    rm -f ${auth_dir}/askpass && rmdir ${auth_dir}
    END
