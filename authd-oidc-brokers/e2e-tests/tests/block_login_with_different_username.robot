*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

Test Tags       robot:exit-on-failure


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Log in with local user
    Log in


Fail to login if usernames do not match
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code   different_user
    Select Provider
    Continue Log In With Remote User: Log In On External Browser   ${username}
    Check That User Information Can Not Be Fetched
