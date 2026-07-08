*** Settings ***
Documentation       Broker resource for the e2e tests.
...
...                 This is the single broker resource file. It defines all
...                 broker-specific variables (loaded from broker_vars.py based
...                 on the BROKER environment variable) and all broker keywords.

Resource            kvm.resource
Resource            resources/utils.resource
Resource            resources/authd.resource
Library             ./Browser.py    AS    Browser
Library             ./TOTP.py    AS    TOTP
Library             Hid.py    AS    Hid
Library             OperatingSystem
Library             String
Variables           ./broker_vars.py


*** Keywords ***
Enable Edge Broker
    SSH.Execute    snap refresh ${BROKER_SNAP_NAME} --edge


Disable Entra Password Via Drop In
    [Documentation]    Writes a drop-in that sets entra_password = false.
    ...
    ...                More robust than Change Broker Configuration for this key,
    ...                because stable snapshots provisioned before the [flows]
    ...                section was added may not contain an entra_password line
    ...                at all, causing a silent sed no-op.
    # TODO(stable-release): Can be removed once the stable broker has been
    #                       updated to include the [flows] section.
    SSH.Execute    mkdir -p ${BROKER_CFG_DIR}
    SSH.Execute    printf '[flows]\nentra_password = false\n' > ${BROKER_CFG_DIR}/99-disable-entra-password.conf
    SSH.Execute    snap restart ${BROKER_SNAP_NAME}


Disable Broker And Purge Config
    SSH.Execute    snap stop ${BROKER_SNAP_NAME}
    SSH.Execute    rm ${AUTHD_BROKER_CFG}
    SSH.Execute    systemctl restart authd.service


Log In With Remote User Through CLI: QR Code
    [Arguments]    ${username}    ${local_password}
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser
    Continue Log In With Remote User Through CLI: Define Local Password    ${username}    ${local_password}


Start Log In With Remote User Through CLI: QR Code
    [Arguments]    ${username}
    Try machinectl login Prompt
    Hid.Type String    ${username}
    Hid.Keys Combo    Return

    Match Text    Select your provider    15


Select Provider
    Match Text    Select your provider    15
    Match Text    2. ${PROVIDER_DISPLAY_NAME}

    Hid.Type String    2
    Builtin.Sleep    2


Regenerate QR Code
    Match Text    Request new code    120
    Hid.Keys Combo    Return
    Match Text    ${DEVICE_URL}    15


Continue Log In With Remote User: Authenticate In External Browser
    # Wait until the verification URL and login code are displayed
    Match Text    ${DEVICE_URL}
    # Read the user code.
    ${text} =    Read Text
    ${user_code} =    StringUtils.First Match    ${DEVICE_URL_REGEX}    ${text}
    # The OCR sometimes reads spaces in the user code, even though there are none,
    # so remove any spaces.
    ${user_code} =    Remove String    ${user_code}    ${SPACE}
    # The user code is all uppercase, but sometimes the OCR reads some letters as lowercase,
    # so convert it to uppercase to handle that.
    ${user_code} =    String.Convert To Upper Case    ${user_code}

    Browser.Login    ${user_code}    ${SUITE_OUTPUT_DIR}


Continue Log In With Remote User Through CLI: Define Local Password
    [Arguments]    ${username}    ${local_password}
    # The terminal should now be visible and focused again.
    # Check that we're prompted to set a local password.
    Match Text    New password:    120

    # Set a local password
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    # Confirm the local password
    Match Text    Confirm password:
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    # Wait for the login to complete
    Wait For Shell Prompt After Login    ${username}@ubuntu


Wait For Shell Prompt After Login
    [Documentation]    Waits for the shell prompt that indicates a successful login.
    ...
    ...                If the prompt never appears and the test is one of the two
    ...                migration tests that run the stable authd, inspects the journal
    ...                to tell the specific "granted but System error" failure apart
    ...                from an ordinary login failure before attributing it to a known
    ...                bug. An ordinary wrong-password failure is logged by login
    ...                as "Authentication failure", whereas the flaky failure
    ...                tracked in https://github.com/canonical/authd/issues/1642
    ...                is logged as "System error": PAM_SYSTEM_ERR returned by the
    ...                (stable) authd PAM module *after* the broker already
    ...                granted access. The edge/fixed authd never returns that, so
    ...                this check is restricted to the migration tests (the only tests
    ...                that run the stable authd) to avoid masking genuine failures.
    [Arguments]    ${shell_prompt}    ${timeout_sec}=120
    ${status}    ${error} =    Run Keyword And Ignore Error
    ...    Match Text    ${shell_prompt}    ${timeout_sec}
    IF    '${status}' == 'PASS'    RETURN

    # TODO: Remove this #1642 check once the next authd stable release is out,
    # since it includes the fix for https://github.com/canonical/authd/issues/1642.
    # The shell prompt did not appear. Only attribute the failure to #1642 if we are
    # running one of the migration tests (where the stable, unfixed authd is active)
    # and the journal shows login failing with a PAM "System error".
    IF    '${TEST NAME}' == 'Test login after updating authd to edge version' or '${TEST NAME}' == 'Test login after upgrading authd and broker to edge channel'
        ${system_error} =    SSH.Execute
        ...    sudo journalctl --no-pager --since "10 minutes ago" | grep "FAILED LOGIN" | grep "System error" || true
        IF    $system_error != ''
            Skip    Login failed with a PAM "System error" after authentication succeeded. This is the flaky failure tracked in https://github.com/canonical/authd/issues/1642.\n\nMatching journal line(s):\n${system_error}
        END
    END
    Fail    ${error}


Log In With Remote User Through CLI: Local Password
    [Arguments]    ${username}    ${local_password}
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Try machinectl login Prompt
    Hid.Type String    ${username}
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Match Text    regex:Enter\\s*your\\s*(?:local\\s*)?password:    120
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Wait For Shell Prompt After Login    ${username}@ubuntu:~$


Check That su To Local User Goes To Local Broker
    Hid.Type String    su ubuntu
    Hid.Keys Combo    Return
    Match Text    Password:    120
    Hid.Type String    ubuntu    # The password for the local user is the same as the username in our test setup
    Hid.Keys Combo    Return
    Match Text    ubuntu@ubuntu:~$    120


Check That Username Cannot Be Changed When Using su
    [Arguments]    ${username}
    Hid.Type String    su ${username}
    Hid.Keys Combo    Return
    Try Navigating Back to User Selection
    Cancel Operation
    Match Text    su: Critical error - immediate abort    120


Try Navigating Back to User Selection
    # First screen is the autoselected local password mode
    Match Text    regex:Enter\\s*your\\s*(?:local\\s*)?password:    120
    Hid.Keys Combo    Escape

    # The previous screen should be the authentication mode selection
    Match Text    Select your authentication method    120
    Hid.Keys Combo    Escape

    # The previous screen should be the broker selection
    Match Text    Select your provider    120
    Hid.Keys Combo    Escape

    # The previous screen is the username prompt, but it shouldn't be possible to get there
    Match Text    Select your provider    120
    Hid.Keys Combo    Escape
    Match Text    Select your provider    120


Log In With Remote User Through CLI: Local Password And Expect Failure
    [Arguments]    ${username}    ${local_password}
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Try machinectl login Prompt
    Hid.Type String    ${username}
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Match Text    regex:Enter\\s*your\\s*(?:local\\s*)?password:    120
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return
    Check That Remote User Is Not Allowed To Log In
    # Exit the machinectl session (three ^] within 1 s exits the session).
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]
    # Wait until the shell prompt is back before the next login attempt.
    Match Text    @ubuntu:~$    30


Remove Registered Owner
    [Documentation]    Removes the auto-registration drop-in that the broker creates when
    ...               the first user logs in, so that subsequent OWNER-based tests start
    ...               from a known-clean state.
    SSH.Execute    rm -f ${BROKER_CFG_DIR}/20-owner-autoregistration.conf
    SSH.Execute    snap restart ${BROKER_SNAP_NAME}


Block Network Access To Identity Provider
    [Documentation]    Blocks outbound HTTPS traffic (IPv4 and IPv6) to simulate the identity
    ...               provider being unreachable. The iptables rules are automatically reverted
    ...               when the VM snapshot is restored at the start of the next test.
    SSH.Execute    iptables -I OUTPUT -p tcp --dport 443 -j REJECT
    SSH.Execute    ip6tables -I OUTPUT -p tcp --dport 443 -j REJECT


# Uses sed to change the broker configuration.
# It should match both commented and uncommented lines.
# The full command looks like:
#     sed -i 's/#*${config_key} = .*/${config_key} = ${config_value}/g' ${BROKER_CFG}
Change Broker Configuration
    [Arguments]    ${config_key}    ${config_value}
    SSH.Execute    sed -i 's|^#*${config_key} =.*|${config_key} = ${config_value}|g' ${BROKER_CFG}
    SSH.Execute    snap restart ${BROKER_SNAP_NAME}


# Pretty much the same as the function above, but since YARF does not have support for
# shifted characters yet, we need to do some workarounds.
Change allowed_users In Broker Configuration
    [Arguments]    ${config_value}
    SSH.Execute    sed -i 's/^#*allowed_users =.*/allowed_users = ${config_value}/g' ${BROKER_CFG}
    SSH.Execute    snap restart ${BROKER_SNAP_NAME}


Comment Key In Broker Configuration
    [Arguments]    ${config_key}
    SSH.Execute    sed -i 's/^#*${config_key} =.*/#${config_key} = .*/g' ${BROKER_CFG}
    SSH.Execute    snap restart ${BROKER_SNAP_NAME}


Log In With Remote User Through SSH: QR Code
    [Arguments]    ${username}    ${local_password}
    Start Log In With Remote User Through SSH: QR Code    ${username}
    Select Provider through SSH
    Continue Log In With Remote User: Authenticate In External Browser
    Continue Log In With Remote User Through SSH: QR Code
    Continue Log In With Remote User Through SSH: Define Local Password    ${username}    ${local_password}


Start Log In With Remote User Through SSH: QR Code
    [Arguments]    ${username}
    Hid.Type String    ssh ${username}@localhost
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Hid.Type String    yes
    Hid.Keys Combo    Return


Select Provider through SSH
    Match Text    2. ${PROVIDER_DISPLAY_NAME}    120
    Match Text    Choose your provider:    120
    Hid.Type String    2
    Hid.Keys Combo    Return


Continue Log In With Remote User Through SSH: QR Code
    Match Text    Choose action:    120
    Hid.Type String    1
    Hid.Keys Combo    Return


Continue Log In With Remote User Through SSH: Define Local Password
    [Arguments]    ${username}    ${local_password}
    Match Text    Create a local password:    120
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Match Text    Confirm Password:    120
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Match Text    ${username}@ubuntu:~$    120


Log In With Remote User Through SSH: Local Password
    [Arguments]    ${username}    ${local_password}
    Hid.Type String    ssh ${username}@localhost
    Hid.Keys Combo    Return
    Match Text    regex:Enter\\s*your\\s*(?:local\\s*)?password:    120
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return
    Match Text    ${username}@ubuntu:~$    120


Log In With Remote User Through GDM: QR Code
    [Arguments]    ${username}    ${local_password}
    Start Log In With Remote User Through GDM: QR Code    ${username}
    Select Broker Through GDM
    Continue Log In With Remote User: Authenticate In External Browser
    Continue Log In With Remote User Through GDM: Define Local Password    ${local_password}


Start Log In With Remote User Through GDM: QR Code
    [Arguments]    ${username}
    Wait Until GDM Login Screen Ready
    Move Pointer To Not listed
    Left Button Click

    # Enter the username
    Match Text    Username
    Hid.Type String    ${username}
    Hid.Keys Combo    Return


Select Broker Through GDM
    Match Text    Select the broker    120
    Move Pointer To ${PROVIDER_DISPLAY_NAME}
    Left Button Click
    Builtin.Sleep    1


Continue Log In With Remote User Through GDM: Define Local Password
    [Arguments]    ${local_password}
    Match Text    Create a local password    120
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Match Text   Please, type the new passphrase again    120
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Wait Until Desktop Ready


Log In With Remote User Through GDM: Local Password
    [Arguments]    ${username}    ${local_password}
    Wait Until GDM Login Screen Ready
    Move Pointer To Not listed
    Left Button Click
    # Enter the username
    Match Text    Username
    Hid.Type String    ${username}
    Hid.Keys Combo    Return

    # Enter the password
    Match Text    Password
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Wait Until Desktop Ready


Check User Information
    [Arguments]    ${username}
    ${output} =    SSH.Execute    getent passwd ${username}
    Should Contain    ${output}    ${username}:x:


Check Configuration Value
    [Arguments]    ${config_key}    ${expected_value}
    ${content} =    SSH.Execute    grep ${config_key} ${BROKER_CFG}
    Should Contain    ${content}    ${expected_value}


Check If Owner Was Registered
    [Arguments]    ${username}
    ${content} =    SSH.Execute    cat ${BROKER_CFG_DIR}/20-owner-autoregistration.conf
    Should Contain    ${content}    owner = ${username}


Check Home Directory
    [Arguments]    ${username}    ${base_dir}=/home
    ${home_dir} =    SSH.Execute    getent passwd ${username} | cut -d: -f6
    Should Start With    ${home_dir}    ${base_dir}/
    ${ownership} =    SSH.Execute    stat -c %U:%G ${home_dir}
    Should Be Equal    ${ownership}    ${username}:${username}


Check That Remote User Is Not Allowed To Log In
    Match Text    authentication failure: user not allowed in broker configuration    120


Check That Login Is Handled By PAM Unix
    Match Text    Password:


Check That SSH Connection Is Closed
    Match Text    Connection closed


Check If User Was Added Properly
    [Arguments]    ${username}
    Wait Until Keyword Succeeds    30s    1s    Check User Information    ${username}
    IF    '${BROKER_SNAP_NAME}' == 'authd-msentraid'
        Wait Until Keyword Succeeds    120s    1s    Check User Groups    ${username}    ${remote_group}
        Check That Remote User Can Run Sudo Commands    ${local_password}
    END

Check User Groups
    [Arguments]    ${username}    ${remote_group}
    ${output} =    SSH.Execute    groups ${username}
    Should Contain    ${output}    sudo
    Should Contain    ${output}    ${remote_group}


Enable Device Registration
    [Documentation]    Turns on automatic device registration in the broker config.
    ...               The 'register_device' key lives in the '[msentraid]' section and is
    ...               only meaningful for the msentraid broker.
    Change Broker Configuration    register_device    true


Check That Device Was Registered
    [Documentation]    Verifies that device registration data was persisted for the user.
    ...
    ...               On successful registration the broker stores the device registration
    ...               data in the user's cached token at
    ...               <SNAP_DATA>/<issuer>/<username>/token.json. The data is held in the
    ...               'DeviceRegistrationData' field, which is null when the device is not
    ...               registered and a (base64-encoded) string once it is. We assert over
    ...               SSH because the on-disk state is the authoritative outcome and is not
    ...               observable on screen.
    [Arguments]    ${username}
    # The issuer (tenant-specific) is part of the path, so glob across issuer dirs.
    # 'current' is a symlink to the snap revision, so '-L' is needed to descend
    # into it.
    ${token_path} =    SSH.Execute
    ...    sudo find -L /var/snap/${BROKER_SNAP_NAME}/current -path '*/${username}/token.json'
    Should Not Be Empty    ${token_path}
    ...    msg=No token.json found for ${username}; device auth did not cache a token.
    # DeviceRegistrationData is null when the device is not registered and a
    # (base64-encoded) string once it is.
    ${registered} =    SSH.Execute
    ...    sudo jq -r '.DeviceRegistrationData != null and .DeviceRegistrationData != ""' '${token_path}'
    Should Be Equal    ${registered}    true
    ...    msg=token.json has no DeviceRegistrationData; the device was not registered.


Log In With Remote User Through CLI: Entra Password
    [Arguments]    ${username}
    Try machinectl login Prompt
    Hid.Type String    ${username}
    Hid.Keys Combo    Return

    Match Text    Select your provider    15
    Match Text    2. ${PROVIDER_DISPLAY_NAME}
    Hid.Type String    2

    # With the device code flow disabled there is only one flow; the PAM module
    # auto-selects it and goes directly to the Entra ID password prompt.
    Match Text    Enter your Entra ID password:    30    similarity=90
    Hid.Type String    %{E2E_PASSWORD}
    Hid.Keys Combo    Return

    # Wait for the MFA code prompt and enter a fresh TOTP code.
    Match Text    Enter your MFA code:    120    similarity=88
    ${totp_code} =    TOTP.Generate Totp Code
    Hid.Type String    ${totp_code}
    Hid.Keys Combo    Return

    # Wait for the authenticated shell prompt. Plain Match Text is fuzzy enough
    # to false-positive on the preceding machinectl login line, so keep this in
    # regex mode and require the remote-shell prefix after the username.
    Match Text    regex:${username}@ubuntu:    120
