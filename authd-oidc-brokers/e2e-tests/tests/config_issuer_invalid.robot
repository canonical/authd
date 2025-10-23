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
    Log In


Change broker configuration to an invalid issuer
    Change Broker Configuration    issuer    invalid


Try to log in with remote user when broker has invalid issuer
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Check That Remote User Has No Available Authentication Modes
