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
Library             Hid.py    AS    Hid
Library             OperatingSystem
Library             String
Variables           ./broker_vars.py


*** Keywords ***
Enable Edge Broker
    SSH.Execute    sudo snap refresh ${BROKER_SNAP_NAME} --edge


Disable Broker And Purge Config
    SSH.Execute    sudo snap stop ${BROKER_SNAP_NAME}
    SSH.Execute    sudo rm ${AUTHD_BROKER_CFG}
    SSH.Execute    sudo systemctl restart authd.service


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
    ${timeout_sec} =    Set Variable    120
    Match Text    ${username}@ubuntu    ${timeout_sec}


Log In With Remote User Through CLI: Local Password
    [Arguments]    ${username}    ${local_password}
    Try machinectl login Prompt
    Hid.Type String    ${username}
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Match Text    Enter your local password:    120
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Match Text    ${username}@ubuntu:~$    120


# Uses sed to change the broker configuration.
# It should match both commented and uncommented lines.
# The full command looks like:
#     sed -i 's/#*${config_key} = .*/${config_key} = ${config_value}/g' ${BROKER_CFG}
Change Broker Configuration
    [Arguments]    ${config_key}    ${config_value}
    SSH.Execute    sudo sed -i 's/^#*${config_key} =.*/${config_key} = ${config_value}/g' ${BROKER_CFG}
    SSH.Execute    sudo snap restart ${BROKER_SNAP_NAME}


# Pretty much the same as the function above, but since YARF does not have support for
# shifted characters yet, we need to do some workarounds.
Change allowed_users In Broker Configuration
    [Arguments]    ${config_value}
    SSH.Execute    sudo sed -i 's/^#*allowed_users =.*/allowed_users = ${config_value}/g' ${BROKER_CFG}
    SSH.Execute    sudo snap restart ${BROKER_SNAP_NAME}


Comment Key In Broker Configuration
    [Arguments]    ${config_key}
    SSH.Execute    sudo sed -i 's/^#*${config_key} =.*/#${config_key} = .*/g' ${BROKER_CFG}
    SSH.Execute    sudo snap restart ${BROKER_SNAP_NAME}


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
    Match Text    Enter your local password:    120
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
    Match Text    Not listed    180
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
    Match Text    Not listed    180
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
    Match Text    ${username}:x:    120


Check Configuration Value
    [Arguments]    ${config_key}    ${expected_value}
    Hid.Type String    cat ${BROKER_CFG}
    # TODO: Even though the Hid.Type String works for most characters now, it seems like there are still issues with
    # some special characters like `|`, so we still need the workaround here.
    Hid.Keys Combo    Shift_L    |
    Hid.Type String    grep ${config_key}
    Hid.Keys Combo    Return
    Match Text    ${expected_value}    120


Check If Owner Was Registered
    [Arguments]    ${username}
    Hid.Type String    cat ${BROKER_CFG_DIR}/20-owner-autoregistration.conf
    Hid.Keys Combo    Return
    Match Text    owner = ${username}    120


Check Home Directory
    [Arguments]    ${username}
    Hid.Type String    echo $HOME
    Hid.Keys Combo    Return
    Match Text    /home/${username}    120
    Hid.Type String    stat -c %U:%G $HOME
    Hid.Keys Combo    Return
    Match Text    ${username}:${username}


Check That Remote User Is Not Allowed To Log In
    Match Text    authentication failure: user not allowed in broker configuration    120


Check That Login Is Handled By PAM Unix
    Match Text    Password:


Check That SSH Connection Is Closed
    Match Text    Connection closed


Check If User Was Added Properly
    [Arguments]    ${username}
    Get NSS Passwd Entry For Remote User    ${username}
    Check User Information    ${username}
    IF    '${BROKER_SNAP_NAME}' == 'authd-msentraid'
        Get NSS Group Entries For Remote User    ${username}
        Check User Groups    ${username}    ${remote_group}
        Check That Remote User Can Run Sudo Commands    ${local_password}
    END

Check User Groups
    [Arguments]    ${username}    ${remote_group}
    Match Text    ${username} sudo ${remote_group}    120
