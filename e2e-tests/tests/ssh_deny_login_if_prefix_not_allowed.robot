*** Settings ***
Resource        ./resources/authd/utils.resource
Resource        ./resources/authd/authd.resource

Resource        ./resources/broker/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    Test Setup
Test Teardown   Test Teardown


*** Keywords ***
Test Setup
    Restore Snapshot    %{BROKER}-edge-configured
    Journal.Start Receiving Journal
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}

Test Teardown
    Journal.Stop Receiving Journal
    Journal.Log Journal
    Log Videos On Error

*** Variables ***
${local_password}    qwer1234
${remote_group}    %{E2E_USER}-group


*** Test Cases ***
Test that login is denied if user is not allowed to log in via SSH
    [Documentation]    Test that login via SSH is denied when the user is not allowed by the ssh_allowed_suffixes_first_auth setting.

    # Log in with local user
    Log In

    # Try to log in with not allowed remote user with device authentication through SSH
    ${domain} =    Fetch From Right    %{E2E_USER}    @
    ${username} =    Set Variable    other-user@${domain}
    Open Terminal
    Start Log In With Remote User Through SSH: QR Code    ${username}
    Check That Login Is Handled By PAM Unix
