*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Tags         requires:msentraid
Test Setup    Test Setup
Test Teardown   Test Teardown


*** Variables ***
${username}    %{E2E_USER}


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Restore Broker

Test Teardown
    Restore Broker
    utils.Test Teardown


*** Test Cases ***
Test unknown user login reports a configured broker that is not started
    [Documentation]    Verify that a configured but stopped broker remains
    ...    selectable and reports a clear error when selected.

    Log In
    Stop Broker

    Open Terminal
    Try Log In With Remote User    ${username}
    Select Provider
    Check That Broker Is Unavailable
    Close Focused Window


Test authd user cannot log in when the broker has no tenant
    [Documentation]    Verify that removing the tenant issuer prevents the
    ...    broker from authenticating an authd user.

    Log In
    Remove Broker Configuration Key    issuer
    Run Keyword And Ignore Error    SSH.Execute    snap restart ${BROKER_SNAP_NAME}
    SSH.Execute    systemctl restart authd.service

    Open Terminal
    Try Log In With Remote User    ${username}
    Check That User Is Redirected To Local Broker
    Close Focused Window
