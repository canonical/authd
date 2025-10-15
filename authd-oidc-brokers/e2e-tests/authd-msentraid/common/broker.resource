*** Settings ***
Documentation       MS Entra ID specific resources for the tests

Resource            kvm.resource
Resource            ${AUTHD_COMMON}/utils.resource

Library            Process

*** Variables ***
${AUTHD_COMMON}    ${CURDIR}/../authd-common
${ENTRAID_COMMON}    ${CURDIR}/../broker-common

${AUTHD_BROKER_CFG}    /etc/authd/brokers.d/msentraid.conf
${ENTRAID_BROKER_CFG}    /var/snap/authd-msentraid/current/broker.conf
${ENTRAID_BROKER_CFG_DIR}    /var/snap/authd-msentraid/current/broker.conf.d


*** Keywords ***
Enable Edge Broker
    Open Terminal In Sudo Mode
    Run Command In Terminal    snap refresh authd-msentraid --edge
    Close Terminal In Sudo Mode


Disable Broker And Purge Config
    Open Terminal In Sudo Mode
    Run Command In Terminal    snap stop authd-msentraid
    Run Command In Terminal    rm ${AUTHD_BROKER_CFG}
    Run Command In Terminal    systemctl restart authd.service
    Close Terminal In Sudo Mode


Log In With Remote User Through CLI: QR Code
    [Arguments]    ${username}    ${local_password}
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Continue Log In With Remote User: Log In On External Browser    ${username}
    Continue Log In With Remote User Through CLI: Define Local Password    ${username}    ${local_password}


Start Log In With Remote User Through CLI: QR Code
    [Arguments]    ${username}
    Hid.Type String    machinectl login
    Hid.Keys Combo    Return
    Match Text    ubuntu login:    60
    Hid.Type String    ${username}
    Hid.Keys Combo    Return

    # Check that the provider selection contains the Entra ID provider
    Match Text    Select your provider    15


Select Provider
    # Check that the provider selection contains the Entra ID provider
    Match Text    Select your provider    15
    Match Text    2. Microsoft Entra ID

    # Select the Entra ID provider
    Hid.Type String    2
    Builtin.Sleep    2

Regenerate QR Code
    # As long as we are in the login process, we can regenerate the QR code
    Match Text    Request new login code    30
    Hid.Keys Combo    Return
    Match Text    https://microsoft.com/devicelogin    15


Continue Log In With Remote User: Log In On External Browser
    [Arguments]    ${username}
    # Wait until the verification URL and login code are displayed
    Match Text    https://microsoft.com/devicelogin
    # Read the user code.
    ${text} =    Read Text
    ${user_code} =    StringUtils.FirstMatch    (https://)?microsoft.com/devicelogin\n((Login code: )?([A-Z0-9]+))    ${text}

    Start Process    ${ENTRAID_COMMON}/BrowserWindow.py    ${username}    %{E2E_PASSWORD}    ${user_code}    alias=RemoteAuth
    Wait For Process    RemoteAuth
    Builtin.Sleep    5


Continue Log In With Remote User Through CLI: Define Local Password
    [Arguments]    ${username}    ${local_password}
    # The terminal should now be visible and focused again.
    # Check that we're prompted to set a local password.
    Match Text    New password:    60

    # Set a local password
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    # Confirm the local password
    Match Text    Confirm password:
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    # Wait for the login to complete
    ${timeout_sec} =    Set Variable    30
    Match Text    ${username}@ubuntu    ${timeout_sec}


Log In With Remote User Through CLI: Local Password
    [Arguments]    ${username}    ${local_password}
    Hid.Type String    machinectl login
    Hid.Keys Combo    Return
    Match Text    ubuntu login:    60
    Hid.Type String    ${username}
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Match Text    Enter your local password:    30
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Match Text    ${username}@ubuntu:~$    30


# Uses sed to change the broker configuration.
# It should match both commented and uncommented lines.
# The full commands looks like:
#     sed -i 's/#*${config_key} = .*/${config_key} = ${config_value}/g' ${ENTRAID_BROKER_CFG}
Change Broker Configuration
    [arguments]    ${config_key}    ${config_value}
    Open Terminal In Sudo Mode
    Hid.Type String    sed -i 's/#*${config_key} = .*/${config_key} = ${config_value}/g' ${ENTRAID_BROKER_CFG}
    Hid.Keys Combo    Return
    Run Command In Terminal    snap restart authd-msentraid
    Close Terminal In Sudo Mode


# Pretty much the same as the function above, but since YARF does not have support for
# shifted characters yet, we need to do some workarounds.
Change allowed_users In Broker Configuration
    [arguments]    ${config_value}
    Run Command In Terminal    sudo sed -i 's/#*allowed_users = .*/allowed_users = ${config_value}/g' ${ENTRAID_BROKER_CFG}
    Run Command In Terminal    snap restart authd-msentraid


Comment Key In Broker Configuration
    [arguments]    ${config_key}
    Run Command In Terminal    sudo sed -i 's/#*${config_key} = .*/#${config_key} = ${config_value}/g' ${ENTRAID_BROKER_CFG}
    Run Command In Terminal    snap restart authd-msentraid


Log In With Remote User Through SSH: QR Code
    [Arguments]    ${username}    ${local_password}
    Start Log In With Remote User Through SSH: QR Code    ${username}
    Select Provider through SSH
    Continue Log In With Remote User: Log In On External Browser    ${username}
    Continue Log In With Remote User Through SSH: QR Code
    Continue Log In With Remote User Through SSH: Define Local Password    ${local_password}


Start Log In With Remote User Through SSH: QR Code
    [Arguments]    ${username}
    Hid.Type String    ssh ${username}@localhost
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Hid.Type String    yes
    Hid.Keys Combo    Return


Select Provider through SSH
    Match Text    2. Microsoft Entra ID    30
    Match Text    Choose your provider:    30
    Hid.Type String    2
    Hid.Keys Combo    Return


Continue Log In With Remote User Through SSH: QR Code
    Match Text    Choose action:    30
    Hid.Type String    1
    Hid.Keys Combo    Return


Continue Log In With Remote User Through SSH: Define Local Password
    [Arguments]    ${local_password}
    Match Text    Create a local password:    30
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Match Text    Confirm Password:    30
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Match Text    ${username}@ubuntu:~$    30


Log In With Remote User Through SSH: Local Password
    [Arguments]    ${username}    ${local_password}
    Hid.Type String    ssh ${username}@localhost
    Hid.Keys Combo    Return
    Match Text    Enter your local password:    30
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return
    Match Text    ${username}@ubuntu:~$    30


Log In With Remote User Through GDM: QR Code
    [Arguments]    ${username}    ${local_password}
    Start Log In With Remote User Through GDM: QR Code    ${username}
    Select Broker Through GDM
    Continue Log In With Remote User: Log In On External Browser    ${username}
    Continue Log In With Remote User Through GDM: Define Local Password    ${local_password}



Start Log In With Remote User Through GDM: QR Code
    [Arguments]    ${username}
    Match Text    Not listed    180
    Move Pointer To Not Listed
    Left Button Click

    # Enter the username
    Match Text    Username
    Hid.Type String    ${username}
    Hid.Keys Combo    Return


Select Broker Through GDM
    Match Text    Select the broker    30
    Move Pointer To Microsoft Entra ID
    Left Button Click
    Builtin.Sleep    1


Continue Log In With Remote User Through GDM: Define Local Password
    [Arguments]    ${local_password}
    Match Text    Create a local password    30
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Match Text   Please, type the new passphrase again    30
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Hid.Move Pointer To Proportional    0    1
    Match Text    Show Apps    60


Log In With Remote User Through GDM: Local Password
    [Arguments]    ${username}    ${local_password}
    Match Text    Not listed    180
    Move Pointer To Not Listed
    Left Button Click
    # Enter the username
    Match Text    Username
    Hid.Type String    ${username}
    Hid.Keys Combo    Return

    # Enter the password
    Match Text    Password
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return

    Hid.Move Pointer To Proportional    0    1
    Match Text    Show Apps    60


Check User Information
    [Arguments]    ${username}
    Match Text    ${username}:x:    30


Check User Groups
    [Arguments]    ${username}    ${remote_group}
    Match Text    ${username} sudo ${remote_group}    30


Check Configuration Value
    [Arguments]    ${config_key}    ${expected_value}
    Hid.Type String    cat ${ENTRAID_BROKER_CFG}
    # TODO: Even though the Hid.Type String works for most characters now, it seems like there are still issues with
    # some special characters like `|`, so we still need the workaround here.
    Hid.Keys Combo    Shift_L    |
    Hid.Type String    grep ${config_key}
    Hid.Keys Combo    Return
    Match Text    ${expected_value}    30


Check If Owner Was Registered
    [Arguments]    ${username}
    Hid.Type String    cat ${ENTRAID_BROKER_CFG_DIR}/20-owner-autoregistration.conf
    Hid.Keys Combo    Return
    Match Text    owner = ${username}    30


Check Home Directory
    [Arguments]    ${username}
    Hid.Type String    echo $HOME
    Hid.Keys Combo    Return
    Match Text    /home/${username}    30
    Hid.Type String    ls -l
    Hid.Keys Combo    Return
    Match Text    ${username} ${username}


Check That Remote User Is Not Allowed To Log In
    Match Text    authentication failure: user not allowed in broker configuration    60
