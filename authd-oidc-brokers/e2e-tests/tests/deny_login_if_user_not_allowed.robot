*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

Test Tags       robot:exit-on-failure


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Log in with local user
    Log In


Change owner to another user
    Change Broker Configuration    owner    different-user


Log in with remote user with device authentication
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Continue Log In With Remote User: Log In On External Browser    ${username}
    Check That Remote User Is Not Allowed To Log In
