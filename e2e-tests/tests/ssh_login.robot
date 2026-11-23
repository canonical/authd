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

Test first SSH login keeps reserved IDs across authd restart
    [Documentation]    A pending first-time SSH connection keeps its UID and GID
    ...    across an authd service restart.
    ...
    ...    Pause at provider selection, then restart ``authd.service`` without
    ...    closing SSH. Check that systemd preserves its descriptor count and
    ...    that NSS still returns the same reservation. Cancel the original SSH
    ...    prompt and check that systemd releases the pending descriptors.
    ...
    ...    Read the reserved UID from the journal, then query NSS by ID.
    ...    A name lookup from ``getent`` does not request pre-authentication
    ...    when its parent is ``sshd-session`` rather than ``sshd``.
    ...
    ...    This exercises ``FileDescriptorStorePreserve=yes`` and
    ...    ``NFileDescriptorStore`` with real systemd, not a descriptor-store mock.
    ...    The broker allows the E2E account through
    ...    ``ssh_allowed_suffixes_first_auth``.
    Log In
    Open Terminal
    ${initial_count} =    Get Authd Descriptor Count
    Start Log In With Remote User Through SSH: QR Code    ${username}
    Match Text    Choose your provider:    120
    ${pending_count} =    Get Authd Descriptor Count
    Should Be True    int($pending_count) > int($initial_count)
    ${reserved_uid} =    Get Reserved UID    ${username}
    ${reserved_entry} =    SSH.Execute    getent passwd ${reserved_uid}
    ${fields} =    Split String    ${reserved_entry}    :
    Should Be Equal    ${fields}[3]    ${reserved_uid}
    Should Be Equal    ${fields}[4]    ${username}

    SSH.Execute    systemctl restart authd.service
    Check Authd Descriptor Count    ${pending_count}
    ${restored_entry} =    SSH.Execute    getent passwd ${reserved_uid}
    Should Be Equal    ${restored_entry}    ${reserved_entry}

    Hid.Keys Combo    Control_L    c
    Wait For Authd Descriptor Count    ${initial_count}


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}

Get Reserved UID
    [Arguments]    ${username}
    ${journal} =    SSH.Execute
    ...    journalctl -b -u authd.service --no-pager -o cat --grep='Added temporary record for user '
    ${escaped_username} =    Regexp Escape    ${username}
    ${reserved_uid} =    StringUtils.First Match
    ...    Added temporary record for user "${escaped_username}" with UID ([0-9]+)
    ...    ${journal}
    RETURN    ${reserved_uid}
