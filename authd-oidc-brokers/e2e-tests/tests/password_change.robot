*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

Test Tags       robot:exit-on-failure

Suite Setup    Restore Snapshot    %{BROKER}-edge-configured
Test Teardown    Log Videos On Error

*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${new_password}    passwd1234
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Log in with local user
    Log In


Log in with remote user with device authentication
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window


Change local password of remote user
    Open Terminal In Sudo Mode
    Change Local Password Of Remote User    ${username}    ${local_password}    ${new_password}
    Close Terminal In Sudo Mode


Log in with remote user with local password
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${new_password}
