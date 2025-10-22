*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

Test Tags       robot:exit-on-failure


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Log in with local user
    Log in


Disable broker
    Disable Broker And Purge Config


Ensure local sudo user can still log in
    Open Terminal
    Enter Sudo Mode In Terminal
    Close Terminal In Sudo Mode


Check that remote user can't log in
    Open Terminal In Sudo Mode
    Try Log In With Remote User    ${username}
    Check That User Is Redirected To Local Broker
    Cancel Operation
    Close Terminal In Sudo Mode
