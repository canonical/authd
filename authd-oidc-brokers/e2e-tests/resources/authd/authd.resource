*** Settings ***
Documentation       Authd related test resources

Resource            ../kvm.resource
Resource            ./utils.resource


*** Variables ***
${local_password}    qwer1234


*** Keywords ***
Enable Edge Repository For Authd
    SSH.Execute    sudo add-apt-repository ppa:ubuntu-enterprise-desktop/authd-edge -y


Disable Authd Socket And Service
    SSH.Execute    sudo systemctl mask --now authd.socket
    SSH.Execute    sudo systemctl mask --now authd.service


Get NSS Passwd Entry For Remote User
    [Arguments]    ${username}
    Hid.Type String    getent passwd ${username}
    Hid.Keys Combo    Return


Get NSS Group Entries For Remote User
    [Arguments]    ${username}
    Hid.Type String    groups ${username}
    Hid.Keys Combo    Return


Change Password
    [Arguments]    ${local_password}    ${new_password}
    Hid.Type String    passwd
    Hid.Keys Combo    Return

    Match Text    Enter your local password:    15
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
    Hid.Type String    machinectl login
    Hid.Keys Combo    Return
    Match Text    ubuntu login:    60
    Hid.Type String    ${username}
    Hid.Keys Combo    Return


# Conditional checks
Check That Remote User Has No Available Authentication Modes
    Match Text    could not get authentication modes: no authentication modes available for user    30


Check That Log In Fails Because Authd Is Disabled
    Match Text    could not connect to unix:///run/authd.sock    30


Check That User Is Redirected To Local Broker
    Match Text    Password:    30


Check That Authenticated User Does Not Match Requested User
    [Arguments]    ${requested_user}
    Match Text    Authentication failure: requested username "${requested_user}" does not match the authenticated username    30


Check That Remote User Can Run Sudo Commands
    [Arguments]    ${local_password}
    Hid.Type String    sudo touch sudo-test.txt
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Match Text    Enter your local password:    15
    Builtin.Sleep    2
    Hid.Type String    ${local_password}
    Hid.Keys Combo    Return
    Builtin.Sleep    2
    Hid.Type String    ls -l
    Hid.Keys Combo    Return
    Match Text    sudo-test.txt    15
