*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup          Test Setup
Test Teardown       utils.Test Teardown


*** Variables ***
${username}             %{E2E_USER}
${local_password}       qwer1234
${unix_username}        authd-e2e-unix
${session_file}         /tmp/authd-ssh-provider-username


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
    [Documentation]    SSH accepts a provider name that resolves to a different
    ...    stable Unix name through the real NSS module.
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
    ...    unchanged. Both names must resolve to the same NSS entry. An SSH login
    ...    with the provider name must run as the Unix user with that UID and home.

    ${versions}=    SSH.Execute    dpkg-query -W authd openssh-server
    Log    ${versions}
    # Register through SSH so localhost's host key is trusted for the later login.
    Log In
    Open Terminal
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From SSH Session
    Close Focused Window

    # Keep the original UID and home as expectations after the stored name changes.
    ${uid}=    SSH.Execute    id -u ${username}
    Set Test Variable    ${uid}
    ${home}=    SSH.Execute    getent passwd ${username} | cut -d: -f6
    Should Not Be Empty    ${home}
    # End all user processes before changing the stored Unix name offline.
    SSH.Execute    loginctl terminate-user ${username}
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute    test -z "$(pgrep -u ${uid})"
    Give Account A Distinct Unix Username

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
    Open Terminal
    # The provider name must open a session as the differently named Unix user.
    Log In With Remote User Through SSH: Local Password
    ...    ${username}    ${local_password}    expected_username=${unix_username}

    # Check the session identity and file owner.
    Hid.Type String    umask 077; { id -un; id -u; printf '%s\\n' "$HOME"; } > ${session_file}
    Hid.Keys Combo    Return
    ${expected}=    Catenate    SEPARATOR=\n    ${unix_username}    ${uid}    ${home}
    Wait Until Keyword Succeeds    30s    1s    Check SSH Session Identity    ${expected}
    Log Out From SSH Session


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}


Give Account A Distinct Unix Username
    [Documentation]    Model an account whose provider name changed without
    ...    renaming the real IdP account. Keep its UID, home and provider binding.
    SSH.Execute    systemctl stop authd.socket authd.service
    SSH.Execute
    ...    python3 -c 'import sqlite3; db = sqlite3.connect("/var/lib/authd/authd.sqlite3"); cursor = db.execute("UPDATE users SET name = ? WHERE name = ? AND provider_username = ? AND provider_id != ?", ("${unix_username}", "${username.lower()}", "${username.lower()}", "")); assert cursor.rowcount == 1, "Expected one account bound to a provider ID"; db.commit()'
    SSH.Execute    systemctl start authd.socket authd.service


Check SSH Session Identity
    [Arguments]    ${expected}
    ${actual}=    SSH.Execute    cat ${session_file}
    Should Be Equal    ${actual}    ${expected}
    ${owner}=    SSH.Execute    stat -c %u ${session_file}
    Should Be Equal    ${owner}    ${uid}
