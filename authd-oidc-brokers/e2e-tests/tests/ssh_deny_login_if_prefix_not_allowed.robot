*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

Test Tags       robot:exit-on-failure

Suite Setup    Restore Snapshot    %{BROKER}-edge-configured
Test Teardown    Log Videos On Error

*** Variables ***
${username}    another-%{E2E_USER}
${local_password}    qwer1234
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Log in with local user
    Log In


Try to log in with not allowed remote user with device authentication through SSH
    Open Terminal
    Start Log In With Remote User Through SSH: QR Code    ${username}
    Select Provider through SSH
    Continue Log In With Remote User: Log In On External Browser    ${username}
    Continue Log In With Remote User Through SSH: QR Code
    Check That Remote User Is Not Allowed To Log In
