*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Tags         requires:msentraid

Test Setup    Test Setup
Test Teardown   Test Teardown


*** Variables ***
${username}        ${EMPTY}
${entra_password}    ${EMPTY}
${entra_totp_secret}    ${EMPTY}
${uid_attribute}    %{E2E_UNIX_IDS_UID_ATTRIBUTE=${EMPTY}}
${gid_attribute}    %{E2E_UNIX_IDS_GID_ATTRIBUTE=${EMPTY}}
${uid_short_attribute}    Linux_UID
${gid_short_attribute}    Linux_GID
${expected_uid}    %{E2E_UNIX_IDS_EXPECTED_UID=${EMPTY}}
${expected_gid}    %{E2E_UNIX_IDS_EXPECTED_GID=${EMPTY}}
${configured_group}    %{E2E_UNIX_IDS_GROUP=${EMPTY}}
${generic_test_group}    %{E2E_UNIX_IDS_GENERIC_GROUP=${EMPTY}}
${expected_ugid}    %{E2E_UNIX_IDS_EXPECTED_UGID=${EMPTY}}
${no_gid_expected_uid}    %{E2E_UNIX_IDS_NO_GID_EXPECTED_UID=${EMPTY}}
${fixture_started}    ${False}


*** Keywords ***
Test Setup
    [Arguments]    ${use_extension_user}=${True}
    IF    not $uid_attribute or not $gid_attribute or not $expected_uid or not $expected_gid or not $configured_group
        Skip    Configure the tenant-specific Entra Unix ID fixture values in e2e-tests-msentraid.env.
    END
    ${shared_user}=    Get Environment Variable    E2E_USER    ${EMPTY}
    ${shared_password}=    Get Environment Variable    E2E_PASSWORD    ${EMPTY}
    ${shared_totp}=    Get Environment Variable    TOTP_SECRET    ${EMPTY}
    ${extension_user}=    Get Environment Variable    E2E_UNIX_IDS_USER    ${EMPTY}
    IF    $use_extension_user
        ${extension_password}=    Get Environment Variable    E2E_UNIX_IDS_PASSWORD    ${EMPTY}
        ${extension_totp}=    Get Environment Variable    E2E_UNIX_IDS_TOTP_SECRET    ${EMPTY}
        IF    not $extension_user or not $extension_password or not $extension_totp
            Skip    E2E_UNIX_IDS_USER, E2E_UNIX_IDS_PASSWORD, and E2E_UNIX_IDS_TOTP_SECRET must be set.
        END
        Set Test Variable    ${username}    ${extension_user}
        Set Test Variable    ${entra_password}    ${extension_password}
        Set Test Variable    ${entra_totp_secret}    ${extension_totp}
    ELSE
        Set Test Variable    ${username}    ${shared_user}
        Set Test Variable    ${entra_password}    ${shared_password}
        Set Test Variable    ${entra_totp_secret}    ${shared_totp}
    END

    Set Test Variable    ${fixture_started}    ${True}
    utils.Test Setup    snapshot=%{BROKER}-installed

    # Keep the group requirement optional because a user may be a member of
    # other remote groups whose GID extension is intentionally unset.
    Change Broker Configuration    register_device    true
    Change Broker Configuration    unix_uid_attribute    ${uid_attribute}
    Change Broker Configuration    unix_gid_attribute    ${gid_attribute}
    Change Broker Configuration    unix_uid_required    true
    Change Broker Configuration    unix_gid_required    false

Test Setup With Generic Entra Account
    IF    not $generic_test_group
        Skip    Set E2E_UNIX_IDS_GENERIC_GROUP to the shared account's group without a GID in e2e-tests-msentraid.env.
    END
    Set Test Variable    ${configured_group}    ${generic_test_group}
    Test Setup    use_extension_user=${False}

Test Setup With No GID Entra Account
    ${extension_user}=    Get Environment Variable    E2E_UNIX_IDS_NO_GID_USER    ${EMPTY}
    ${extension_password}=    Get Environment Variable    E2E_UNIX_IDS_NO_GID_PASSWORD    ${EMPTY}
    ${extension_totp}=    Get Environment Variable    E2E_UNIX_IDS_NO_GID_TOTP_SECRET    ${EMPTY}
    IF    not $extension_user or not $extension_password or not $extension_totp or not $no_gid_expected_uid or not $generic_test_group
        Skip    Configure the no-GID credentials and fixture values in e2e-tests-msentraid.env.
    END
    Set Test Variable    ${configured_group}    ${generic_test_group}
    Test Setup    use_extension_user=${False}
    Set Test Variable    ${username}    ${extension_user}
    Set Test Variable    ${entra_password}    ${extension_password}
    Set Test Variable    ${entra_totp_secret}    ${extension_totp}

Check Marker File Is Root Owned
    [Arguments]    ${marker_file}
    ${owner}=    SSH.Execute    stat -c %u ${marker_file}
    Should Be Equal As Integers    ${owner}    0

Check Private Group Matches UID
    [Arguments]    ${user}    ${uid}
    ${private_gid}=    SSH.Execute    getent group '${user}' | cut -d: -f3
    Should Be Equal As Strings    ${private_gid}    ${uid}
    ${primary_gid}=    SSH.Execute    id -g '${user}'
    Should Be Equal As Strings    ${primary_gid}    ${uid}

Test Teardown
    IF    $fixture_started
        utils.Test Teardown
    END

Find User Token Cache
    [Arguments]    ${user}
    ${path}=    SSH.Execute
    ...    for f in $(find -L /var/snap/${BROKER_SNAP_NAME}/current -type f -name token.json); do python3 -c 'import json,sys; sys.exit(0 if json.load(open(sys.argv[1])).get("UserInfo",{}).get("name")==sys.argv[2] else 1)' "$f" "${user}" && { printf '%s\n' "$f"; break; }; done
    Should Not Be Empty    ${path}
    RETURN    ${path}


Read Cached UID
    [Arguments]    ${token_path}
    ${output}=    SSH.Execute    python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["UserInfo"].get("uid", ""))' "${token_path}"
    RETURN    ${output}


Read Cached Group Field
    [Arguments]    ${token_path}    ${group_name}    ${field}
    ${output}=    SSH.Execute
    ...    python3 -c 'import json,sys; groups=[g for g in json.load(open(sys.argv[1]))["UserInfo"].get("groups",[]) if g.get("name","").lower()==sys.argv[2].lower()]; assert len(groups)==1, groups; print(groups[0].get(sys.argv[3], ""))' "${token_path}" "${group_name}" "${field}"
    RETURN    ${output}


Start Entra Login
    [Arguments]    ${entra_user}=${username}
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser
    ...    ${entra_user}    ${entra_password}    ${entra_totp_secret}

Retry Entra Login
    [Arguments]    ${entra_user}=${username}
    Hid.Type String    ${username}
    Hid.Keys Combo    Return
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser
    ...    ${entra_user}    ${entra_password}    ${entra_totp_secret}

Should Show Full Required Error
    [Arguments]    ${expected_text}
    ${screen_text}=    Read Text
    Log To Console    OCR screen text: ${screen_text}
    ${expected_pattern}=    Evaluate    '(?s).*' + (chr(92) + 'n?').join(__import__('re').escape(char) for char in $expected_text) + '.*'
    Should Match Regexp    ${screen_text}    ${expected_pattern}

*** Test Cases ***
Test Entra Unix IDs are cached and can be applied
    [Documentation]    Verify the admin workflow for pre-provisioned Entra UID and GID extension attributes.
    ...    The test authenticates the real Entra user, reads the authoritative token.json cache,
    ...    verifies the cache-only UID/GID values, and applies them explicitly with authctl.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code
    ...    ${username}    ${local_password}    ${username}    ${entra_password}    ${entra_totp_secret}
    Check If User Was Added Properly    ${username}    ${local_password}    ${configured_group}

    Wait Until Keyword Succeeds    30s    2s    Find User Token Cache    ${username}
    ${token_path}=    Find User Token Cache    ${username}

    ${cache_mode}=    SSH.Execute    stat -c %a "$(dirname "${token_path}")"
    Should Be Equal As Strings    ${cache_mode}    700
    ${file_mode}=    SSH.Execute    stat -c %a "${token_path}"
    Should Be Equal As Strings    ${file_mode}    600

    ${cached_uid}=    Read Cached UID    ${token_path}
    Should Be Equal As Strings    ${cached_uid}    ${expected_uid}

    ${cached_gid}=    Read Cached Group Field    ${token_path}    ${configured_group}    gid
    Should Be Equal As Strings    ${cached_gid}    ${expected_gid}

    ${cached_ugid}=    Read Cached Group Field    ${token_path}    ${configured_group}    ugid
    Should Not Be Empty    ${cached_ugid}
    IF    $expected_ugid != ''
        Should Be Equal As Strings    ${cached_ugid}    ${expected_ugid}
    END

    # Apply the cached values explicitly after inspecting them.
    Log Out From Terminal Session
    Close Focused Window
    SSH.Execute    loginctl terminate-user ${username} || true
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute    test -z "$(pgrep -u ${username} || true)"

    ${uid_output}=    SSH.Execute    authctl user set-uid '${username}' ${expected_uid} 2>&1
    Should Contain    ${uid_output}    UID of user '${username}' set to ${expected_uid}.

    ${private_gid_output}=    SSH.Execute    authctl group set-gid '${username}' ${expected_uid} 2>&1
    Should Contain    ${private_gid_output}    GID of group '${username}' set to ${expected_uid}.

    ${gid_output}=    SSH.Execute    authctl group set-gid '${configured_group}' ${expected_gid} 2>&1
    Should Contain    ${gid_output}    GID of group '${configured_group}' set to ${expected_gid}.

    ${actual_uid}=    SSH.Execute    getent passwd '${username}' | cut -d: -f3
    Should Be Equal As Strings    ${actual_uid}    ${expected_uid}
    ${actual_gid}=    SSH.Execute    getent group '${configured_group}' | cut -d: -f3
    Should Be Equal As Strings    ${actual_gid}    ${expected_gid}

    Check Private Group Matches UID    ${username}    ${expected_uid}


Test Entra Unix IDs can be applied with the bridge script
    [Documentation]    Verify that apply-entra-unix-ids reads cached Entra UID/GID values,
    ...    shows the planned authctl commands, and applies them when --apply is specified.
    ...    The private group and primary GID must match the UID, including after a repeat application.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code
    ...    ${username}    ${local_password}    ${username}    ${entra_password}    ${entra_totp_secret}
    Check If User Was Added Properly    ${username}    ${local_password}    ${configured_group}

    Wait Until Keyword Succeeds    30s    2s    Find User Token Cache    ${username}
    ${token_path}=    Find User Token Cache    ${username}

    ${cached_uid}=    Read Cached UID    ${token_path}
    Should Be Equal As Strings    ${cached_uid}    ${expected_uid}
    ${cached_gid}=    Read Cached Group Field    ${token_path}    ${configured_group}    gid
    Should Be Equal As Strings    ${cached_gid}    ${expected_gid}

    Log Out From Terminal Session
    Close Focused Window
    SSH.Execute    loginctl terminate-user ${username} || true
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute    test -z "$(pgrep -u ${username} || true)"

    ${script_path}=    Set Variable    /tmp/apply-entra-unix-ids
    SSH.Copy To VM    ${CURDIR}/../../scripts/apply-entra-unix-ids    ${script_path}
    SSH.Execute    chmod 755 ${script_path}

    ${dry_run_output}=    SSH.Execute    ${script_path} --authctl authctl ${token_path}
    Should Contain    ${dry_run_output}    authctl user set-uid ${username} ${expected_uid}
    Should Contain    ${dry_run_output}    authctl group set-gid ${username} ${expected_uid}
    Should Contain    ${dry_run_output}    authctl group set-gid ${configured_group} ${expected_gid}

    ${apply_output}=    SSH.Execute    ${script_path} --apply --authctl authctl ${token_path} 2>&1
    Should Contain    ${apply_output}    Applying cached assignments for ${username}:
    Should Contain    ${apply_output}    UID of user '${username}' set to ${expected_uid}.
    Should Contain    ${apply_output}    GID of group '${username}' set to ${expected_uid}.
    Should Contain    ${apply_output}    GID of group '${configured_group}' set to ${expected_gid}.

    ${actual_uid}=    SSH.Execute    getent passwd '${username}' | cut -d: -f3
    Should Be Equal As Strings    ${actual_uid}    ${expected_uid}
    ${actual_gid}=    SSH.Execute    getent group '${configured_group}' | cut -d: -f3
    Should Be Equal As Strings    ${actual_gid}    ${expected_gid}

    Check Private Group Matches UID    ${username}    ${expected_uid}

    ${repeat_output}=    SSH.Execute    ${script_path} --apply --authctl authctl ${token_path} 2>&1
    Should Contain    ${repeat_output}    User '${username}' already has UID ${expected_uid}.
    Should Contain    ${repeat_output}    Group '${username}' already has GID ${expected_uid}.
    Check Private Group Matches UID    ${username}    ${expected_uid}


Test Offline Login Uses Complete Cached Unix IDs Unless Provider Check Is Forced
    [Documentation]    Verify complete cached required UID/GID values allow offline login when
    ...    force_access_check_with_provider is false, but do not bypass a forced provider check.

    Log In
    Change Broker Configuration    unix_gid_required    true
    Change Broker Configuration    force_access_check_with_provider    false

    Open Terminal
    Log In With Remote User Through CLI: QR Code
    ...    ${username}    ${local_password}    ${username}    ${entra_password}    ${entra_totp_secret}
    Check If User Was Added Properly    ${username}    ${local_password}    ${configured_group}

    ${token_path}=    Find User Token Cache    ${username}
    ${cached_uid}=    Read Cached UID    ${token_path}
    Should Be Equal As Strings    ${cached_uid}    ${expected_uid}
    ${cached_gid}=    Read Cached Group Field    ${token_path}    ${configured_group}    gid
    Should Be Equal As Strings    ${cached_gid}    ${expected_gid}

    Log Out From Terminal Session
    Close Focused Window
    Block Network Access To Identity Provider

    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    ${offline_token_path}=    Find User Token Cache    ${username}
    ${offline_uid}=    Read Cached UID    ${offline_token_path}
    Should Be Equal As Strings    ${offline_uid}    ${expected_uid}
    ${offline_gid}=    Read Cached Group Field    ${offline_token_path}    ${configured_group}    gid
    Should Be Equal As Strings    ${offline_gid}    ${expected_gid}

    Log Out From Terminal Session
    Close Focused Window
    Change Broker Configuration    force_access_check_with_provider    true

    Open Terminal
    Try Log In With Remote User    ${username}
    Check That Remote User Has No Available Authentication Modes


Test User with Entra UID and at least one group without a GID
    [Documentation]    Verify that a user with a valid Entra UID but at least one group without a GID
    ...    will be rejected when GID is required and can log in when GID is optional.
    [Setup]    Test Setup With No GID Entra Account

    Log In
    Open Terminal
    Change Broker Configuration    unix_gid_required    true
    Start Entra Login
    Wait Until Keyword Succeeds    30s    1s    Should Show Full Required Error
    ...    Microsoft Entra ID did not provide a valid required Unix GID for one or more of your groups. Please contact your administrator.
    Match Text    Login incorrect    30

    Change Broker Configuration    unix_gid_required    false
    Retry Entra Login
    Continue Log In With Remote User Through CLI: Define Local Password    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}    ${local_password}    ${configured_group}

    Wait Until Keyword Succeeds    30s    2s    Find User Token Cache    ${username}
    ${token_path}=    Find User Token Cache    ${username}
    ${cached_uid}=    Read Cached UID    ${token_path}
    Should Be Equal As Strings    ${cached_uid}    ${no_gid_expected_uid}
    ${cached_gid}=    Read Cached Group Field    ${token_path}    ${configured_group}    gid
    Should Be Empty    ${cached_gid}


Test GDM login rejects missing group GID then succeeds when optional
    [Documentation]    Verify GDM rejects the ext2 user when a group GID is required,
    ...    then provisions the user when group GIDs are optional.
    [Setup]    Test Setup With No GID Entra Account

    Change Broker Configuration    entra_auth    true
    Change Broker Configuration    device_code    false
    Change Broker Configuration    unix_gid_required    true
    Start Log In With Remote User Through GDM    ${username}
    Select Broker Through GDM
    Enter Entra ID Password And MFA    Match Text    Select the broker    30
    ...    entra_password=${entra_password}    entra_totp_secret=${entra_totp_secret}
    Match Text    Select the broker    30
    ${sessions}=    SSH.Execute    loginctl list-sessions --no-legend | awk '$3 == "${username}" {print}'
    Should Be Empty    ${sessions}

    Change Broker Configuration    unix_gid_required    false
    Select Broker Through GDM
    Continue Log In With Remote User Through GDM: Entra Password    ${entra_password}    ${entra_totp_secret}
    Check If User Was Added Properly    ${username}    ${entra_password}    ${configured_group}


Test polkit authentication rejects missing group GID then succeeds when optional
    [Documentation]    Verify polkit rejects the ext2 user's Entra authentication when a
    ...    group GID is required, then authorizes pkexec when group GIDs are optional.
    [Setup]    Test Setup With No GID Entra Account

    Start Log In With Remote User Through GDM    ${username}
    Select Broker Through GDM
    Continue Log In With Remote User: Authenticate In External Browser
    ...    ${username}    ${entra_password}    ${entra_totp_secret}
    Continue Log In With Remote User Through GDM: Define Local Password    ${local_password}
    Check If User Was Added Properly    ${username}    ${local_password}    ${configured_group}
    SSH.Execute    sudo usermod -aG sudo ${username}

    SSH.Execute    rm -f /tmp/polkit-authd-test-no-gid
    Open Terminal
    Change Broker Configuration    entra_auth    true
    Change Broker Configuration    device_code    false
    Change Broker Configuration    unix_gid_required    true
    Hid.Type String    pkexec touch /tmp/polkit-authd-test-no-gid
    Hid.Keys Combo    Return
    Match Text    Enter your password    60
    Hid.Type String    r
    Hid.Keys Combo    Return
    Match Text    Choose your authentication flow    30
    Match Text    2. Entra ID authentication    15
    Hid.Type String    2
    Hid.Keys Combo    Return
    Enter Entra ID Password And MFA    Match Text    Authentication Required    30
    ...    entra_password=${entra_password}    entra_totp_secret=${entra_totp_secret}
    Match Text    Authentication Required    30
    ${marker_exists}=    SSH.Execute    test -e /tmp/polkit-authd-test-no-gid && echo yes || true
    Should Be Empty    ${marker_exists}

    Move Pointer To Cancel
    Left Button Click
    Change Broker Configuration    unix_gid_required    false
    Hid.Type String    pkexec touch /tmp/polkit-authd-test-no-gid
    Hid.Keys Combo    Return
    Log In With Remote User Through Polkit: Entra Password    ${username}
    ...    entra_password=${entra_password}    entra_totp_secret=${entra_totp_secret}
    Wait Until Keyword Succeeds    30s    5s
    ...    Check Marker File Is Root Owned    /tmp/polkit-authd-test-no-gid


Test Entra MFA login rejects missing group GID then succeeds when optional
    [Documentation]    Verify direct Entra password plus MFA authentication rejects the
    ...    ext2 user when a group GID is required, then succeeds when group GIDs are optional.
    [Setup]    Test Setup With No GID Entra Account

    Change Broker Configuration    entra_auth    true
    Change Broker Configuration    device_code    false
    Change Broker Configuration    unix_gid_required    true
    Log In
    Open Terminal
    Try machinectl login Prompt
    Hid.Type String    ${username}
    Hid.Keys Combo    Return
    Match Text    Select your provider:    15
    Match Text    2. ${PROVIDER_DISPLAY_NAME}
    Hid.Type String    2
    Hid.Keys Combo    Return
    Enter Entra ID Password And MFA    Should Show Full Required Error
    ...    Microsoft Entra ID did not provide a valid required Unix GID for one or more of your groups. Please contact your administrator.
    ...    entra_password=${entra_password}    entra_totp_secret=${entra_totp_secret}
    Wait Until Keyword Succeeds    30s    1s    Should Show Full Required Error
    ...    Microsoft Entra ID did not provide a valid required Unix GID for one or more of your groups. Please contact your administrator.
    Match Text    Login incorrect    30

    Change Broker Configuration    unix_gid_required    false
    Hid.Type String    ${username}
    Hid.Keys Combo    Return
    Match Text    Select your provider:    15
    Match Text    2. ${PROVIDER_DISPLAY_NAME}
    Hid.Type String    2
    Hid.Keys Combo    Return
    Enter Entra ID Password And MFA    Match Text    ${username}@ubuntu:~$    30
    ...    entra_password=${entra_password}    entra_totp_secret=${entra_totp_secret}
    Check If User Was Added Properly    ${username}    ${entra_password}    ${configured_group}

Test Required Entra UID rejects users without a UID, optional UID allows login
    [Documentation]    Verify the generic Entra account without a UID is rejected when
    ...    UID is required and can log in when UID is optional.
    [Setup]    Test Setup With Generic Entra Account

    Log In
    Open Terminal
    Start Entra Login
    Wait Until Keyword Succeeds    30s    1s    Should Show Full Required Error
    ...    Microsoft Entra ID did not provide a valid required Unix UID for your account. Please contact your administrator.
    Match Text    Login incorrect    30

    Change Broker Configuration    unix_uid_required    false
    Retry Entra Login
    Continue Log In With Remote User Through CLI: Define Local Password    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}    ${local_password}    ${configured_group}

    Wait Until Keyword Succeeds    30s    2s    Find User Token Cache    ${username}
    ${token_path}=    Find User Token Cache    ${username}
    ${cached_uid}=    Read Cached UID    ${token_path}
    Should Be Empty    ${cached_uid}


Test Required Entra group GID rejects groups without a GID, optional GID allows login
    [Documentation]    Verify the generic Entra account's test group without a GID is
    ...    rejected when GID is required and can log in when GID is optional.
    [Setup]    Test Setup With Generic Entra Account

    Log In
    Open Terminal
    Change Broker Configuration    unix_uid_required    false
    Change Broker Configuration    unix_gid_required    true
    Start Entra Login
    Wait Until Keyword Succeeds    30s    1s    Should Show Full Required Error
    ...    Microsoft Entra ID did not provide a valid required Unix GID for one or more of your groups. Please contact your administrator.
    Match Text    Login incorrect    30

    Change Broker Configuration    unix_gid_required    false
    Retry Entra Login
    Continue Log In With Remote User Through CLI: Define Local Password    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}    ${local_password}    ${configured_group}

    Wait Until Keyword Succeeds    30s    2s    Find User Token Cache    ${username}
    ${token_path}=    Find User Token Cache    ${username}
    ${cached_gid}=    Read Cached Group Field    ${token_path}    ${configured_group}    gid
    Should Be Empty    ${cached_gid}


Test Entra Unix ID short attribute names resolve correctly
    [Documentation]    Verify short attribute names are resolved against the configured
    ...    Entra application client ID and populate the cached user and group IDs.

    Log In
    Change Broker Configuration    unix_uid_attribute    ${uid_short_attribute}
    Change Broker Configuration    unix_gid_attribute    ${gid_short_attribute}
    Open Terminal
    Log In With Remote User Through CLI: QR Code
    ...    ${username}    ${local_password}    ${username}    ${entra_password}    ${entra_totp_secret}
    Check If User Was Added Properly    ${username}    ${local_password}    ${configured_group}

    Wait Until Keyword Succeeds    30s    2s    Find User Token Cache    ${username}
    ${token_path}=    Find User Token Cache    ${username}
    ${cached_uid}=    Read Cached UID    ${token_path}
    Should Be Equal As Strings    ${cached_uid}    ${expected_uid}
    ${cached_gid}=    Read Cached Group Field    ${token_path}    ${configured_group}    gid
    Should Be Equal As Strings    ${cached_gid}    ${expected_gid}


Test Wrong Entra Unix UID extension name is treated as missing
    [Documentation]    Verify a misspelled required extension name follows the missing-UID
    ...    failure flow instead of silently assigning an incorrect ID.

    [Setup]    Test Setup With Generic Entra Account

    Log In
    Open Terminal
    Change Broker Configuration    unix_uid_attribute    ${uid_short_attribute}_typo
    Start Entra Login
    Wait Until Keyword Succeeds    30s    1s    Should Show Full Required Error
    ...    Microsoft Entra ID did not provide a valid required Unix UID for your account. Please contact your administrator.


Test Wrong Entra Unix GID extension name is treated as missing
    [Documentation]    Verify a misspelled required group extension name follows the missing-GID
    ...    failure flow instead of silently assigning an incorrect ID.

    [Setup]    Test Setup With Generic Entra Account

    Log In
    Open Terminal
    Change Broker Configuration    unix_uid_required    false
    Change Broker Configuration    unix_gid_attribute    ${gid_short_attribute}_typo
    Change Broker Configuration    unix_gid_required    true
    Start Entra Login
    Wait Until Keyword Succeeds    30s    1s    Should Show Full Required Error
    ...    Microsoft Entra ID did not provide a valid required Unix GID for one or more of your groups. Please contact your administrator.