*** Settings ***
Documentation       MS Google specific resources for the tests

Resource            ../kvm.resource
Resource            ../authd/utils.resource
Resource            ../authd/authd.resource
Library             ./Browser.py    AS    Browser
Library             Hid.py    AS    Hid
Library             String

*** Variables ***
${AUTHD_BROKER_CFG}    /etc/authd/brokers.d/google.conf
${GOOGLE_BROKER_CFG}    /var/snap/authd-google/current/broker.conf
${GOOGLE_BROKER_CFG_DIR}    /var/snap/authd-google/current/broker.conf.d


*** Keywords ***
Enable Edge Broker
    SSH.Execute    sudo snap refresh authd-google --edge


Disable Broker And Purge Config
    SSH.Execute    sudo snap stop authd-google
    SSH.Execute    sudo rm ${AUTHD_BROKER_CFG}
    SSH.Execute    sudo systemctl restart authd.service


Log In With Remote User Through CLI: QR Code
    [Arguments]    ${username}    ${local_password}
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Continue Log In With Remote User: Authenticate In External Browser    ${username}
    Continue Log In With Remote User Through CLI: Define Local Password    ${username}    ${local_password}


Start Log In With Remote User Through CLI: QR Code
    [Arguments]    ${username}
    Try machinectl login Prompt
    Hid.Type String    ${username}
    Hid.Keys Combo    Return

    # Check that the provider selection contains the Google provider
    Match Text    Select your provider    15


Select Provider
    # Check that the provider selection contains the Google provider
    Match Text    Select your provider    15
    Match Text    2. Google

    # Select the Google provider
    Hid.Type String    2
    Builtin.Sleep    2

Regenerate QR Code
    # As long as we are in the login process, we can regenerate the QR code
    Match Text    Request new code    120
    Hid.Keys Combo    Return
    Match Text    google.com/device    15


Continue Log In With Remote User: Authenticate In External Browser
    [Arguments]    ${username}
    # Wait until the verification URL and login code are displayed
    Match Text    google.com/device
    # Read the user code.
    ${text} =    Read Text
    ${user_code} =    StringUtils.First Match    (https:\/\/)?google.com\/device\n((Login code: )?(([``?A-Za-z0-9\-?``?]+)))    ${text}
    # The user code is all uppercase, but sometimes the OCR reads some letters as lowercase,
    # so convert it to uppercase to handle that.
    ${user_code} =    String.Convert To Upper Case    ${user_code}

    Browser.Login    ${username}    %{E2E_PASSWORD}    ${user_code}    %{TOTP_SECRET}    ${OUTPUT DIR}


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
# The full commands looks like:
#     sed -i 's/#*${config_key} = .*/${config_key} = ${config_value}/g' ${GOOGLE_BROKER_CFG}
Change Broker Configuration
    [Arguments]    ${config_key}    ${config_value}
    SSH.Execute    sudo sed -i 's/^#*${config_key} =.*/${config_key} = ${config_value}/g' ${GOOGLE_BROKER_CFG}
    SSH.Execute    sudo snap restart authd-google


# Pretty much the same as the function above, but since YARF does not have support for
# shifted characters yet, we need to do some workarounds.
Change allowed_users In Broker Configuration
    [Arguments]    ${config_value}
    SSH.Execute    sudo sed -i 's/^#*allowed_users =.*/allowed_users = ${config_value}/g' ${GOOGLE_BROKER_CFG}
    SSH.Execute    sudo snap restart authd-google


Comment Key In Broker Configuration
    [Arguments]    ${config_key}
    SSH.Execute    sudo sed -i 's/^#*${config_key} =.*/#${config_key} = .*/g' ${GOOGLE_BROKER_CFG}
    SSH.Execute    sudo snap restart authd-google


Log In With Remote User Through SSH: QR Code
    [Arguments]    ${username}    ${local_password}
    Start Log In With Remote User Through SSH: QR Code    ${username}
    Select Provider through SSH
    Continue Log In With Remote User: Authenticate In External Browser    ${username}
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
    Match Text    2. Google    120
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
    Continue Log In With Remote User: Authenticate In External Browser    ${username}
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
    Move Pointer To Google
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


Check If User Was Added Properly
    [Arguments]    ${username}
    Get NSS Passwd Entry For Remote User    ${username}
    Check User Information    ${username}


Check User Information
    [Arguments]    ${username}
    Match Text    ${username}:x:    120


Check Configuration Value
    [Arguments]    ${config_key}    ${expected_value}
    Hid.Type String    cat ${GOOGLE_BROKER_CFG}
    # TODO: Even though the Hid.Type String works for most characters now, it seems like there are still issues with
    # some special characters like `|`, so we still need the workaround here.
    Hid.Keys Combo    Shift_L    |
    Hid.Type String    grep ${config_key}
    Hid.Keys Combo    Return
    Match Text    ${expected_value}    120


Check If Owner Was Registered
    [Arguments]    ${username}
    Hid.Type String    cat ${GOOGLE_BROKER_CFG_DIR}/20-owner-autoregistration.conf
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
