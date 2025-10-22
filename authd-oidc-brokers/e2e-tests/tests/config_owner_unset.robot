*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

Test Tags       robot:exit-on-failure


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Log in with local user
    Log in


Change Broker Configuration to allowed_users as OWNER only
    Change allowed_users In Broker Configuration    OWNER


Try to log in with remote user
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window


Log in with remote user with local password
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Terminal In Sudo Mode


Check That owner Was Updated In Broker Configuration
    Open Terminal In Sudo Mode
    Check Configuration Value    owner    %{username}
