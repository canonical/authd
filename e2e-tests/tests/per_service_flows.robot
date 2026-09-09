*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Setup    Test Setup
Test Teardown   utils.Test Teardown


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    # Device-code authentication is available only to the SSH PAM service.
    # The value is deliberately a service list, not a boolean.
    Change Broker Configuration    device_code    sshd


Machinectl Login Is Rejected
    # PAM reports unavailable authentication modes as a failed login to
    # machinectl. Leave the nested login session before starting SSH.
    Match Text    Login incorrect    120
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]
    Match Text    @ubuntu    30


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test device code flow is scoped to the PAM service
    [Documentation]    Verify that a per-service device-code allowlist is
    ...    enforced using the PAM service name.
    ...
    ...    machinectl login uses the login PAM service and must not receive the
    ...    device-code flow when only sshd is allowed. SSH uses sshd and must
    ...    still receive the flow and complete the first login.

    # Log in with the local user so the desktop terminal is available.
    Log In

    # A non-SSH PAM service must not receive the allowlisted flow.
    Open Terminal
    Start Log In With Remote User Through CLI: QR Code    ${username}
    Select Provider
    Machinectl Login Is Rejected

    # The same flow is available to sshd and can provision the user.
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From SSH Session
    Close Focused Window
