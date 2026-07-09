*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure
Test Tags         requires:msentraid

Test Setup    Test Setup
Test Teardown   utils.Test Teardown


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    # Inject the OIDC client secret into broker.conf at runtime. The base
    # snapshot ships with the secret commented out (so public-client flows are
    # not broken by AADSTS700025); this test is the only one that needs it,
    # because entra_password must stay available with register_device=false
    # by falling back to the app-only Graph token (client credentials).
    ${secret}=    Get Environment Variable    AUTHD_MSENTRAID_CLIENT_SECRET
    Should Not Be Empty    ${secret}    AUTHD_MSENTRAID_CLIENT_SECRET must be set to run this test
    Change Broker Configuration    client_secret    ${secret}
    Change Broker Configuration    register_device    false
    Change Broker Configuration    entra_password    true
    Change Broker Configuration    device_code    false


*** Variables ***
${username}        %{E2E_USER}
# Check If User Was Added Properly uses this cached local password when it
# verifies that sudo prompts for, and accepts, the post-login local password.
${local_password}    %{E2E_PASSWORD}


*** Test Cases ***
Test login with CLI using Entra ID password and MFA with client secret
    [Documentation]    Verify that the Entra ID direct-password + MFA flow works
    ...    through the CLI when device registration is disabled and the broker is
    ...    provisioned with a client secret.
    ...
    ...    The client secret is injected into broker.conf at setup (not baked into
    ...    the snapshot), so the base snapshot stays secret-free for public-client
    ...    flows. This covers the alternate configuration where entra_password stays
    ...    available without register_device=true because Microsoft Graph access
    ...    comes from the configured application secret instead.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: Entra Password    ${username}
    # This shared provisioning check covers NSS, group membership, and the
    # cached local-password path via sudo.
    Check If User Was Added Properly    ${username}

    Wait Until Keyword Succeeds    30s    3s    Check Home Directory    ${username}
