*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

Test Tags       robot:exit-on-failure

Suite Setup    Restore Snapshot    %{BROKER}-stable-configured
Test Teardown    Log Videos On Error


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Test login after updating authd to edge version
    [Documentation]    Test login via CLI with device authentication and local password after switching to the edge PPA for authd.

    # Log in with local user
    Log In

    # Log in with remote user with device authentication
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session
    Close Focused Window

    # Check remote user is properly added to the system
    Open Terminal
    Get NSS Passwd Entry For Remote User    ${username}
    Check User Information    ${username}
    Get NSS Group Entries For Remote User    ${username}
    Check User Groups    ${username}    ${remote_group}
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check That Remote User Can Run Sudo Commands    ${local_password}
    Log Out From Terminal Session
    Close Terminal In Sudo Mode

    # Switch to the edge PPA for authd
    Enable Edge Repository For Authd
    Update And Upgrade Packages

    # Log in with remote user with local password after upgrading
    Open Terminal In Sudo Mode
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check Home Directory    ${username}
