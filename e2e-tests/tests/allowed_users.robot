*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${non_allowed_user}    different-user


*** Test Cases ***
Test allowed_users values with cached local password authentication
    [Documentation]    Verify all allowed_users scenarios with a single device auth.
    ...
    ...    The test registers the remote user once via device authentication (QR code),
    ...    which caches a local password.  All five allowed_users scenarios are then
    ...    exercised using local-password authentication only, so the browser flow is
    ...    not repeated for every scenario.
    ...
    ...    Scenarios covered (in order):
    ...      1. allowed_users=OWNER, owner=<username>       → login succeeds
    ...      2. allowed_users=OWNER, owner=<different-user> → login fails
    ...      3. allowed_users=<username>                    → login succeeds
    ...      4. allowed_users=<non-allowed-user>            → login fails
    ...      5. allowed_users=ALL                           → login succeeds

    Log In

    # Perform device authentication once to register the user and cache their
    # local password.  All subsequent logins reuse this cached password.
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Log Out From Terminal Session

    # The initial login auto-registers the user as owner via a drop-in config.
    # Remove it so that OWNER-based scenarios start from a clean state.
    Remove Registered Owner

    # Scenario 1: OWNER + owner=<username> → success
    Change allowed_users In Broker Configuration    OWNER
    Change Broker Configuration    owner    ${username}
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From Terminal Session

    # Scenario 2: OWNER + owner=different-user → failure
    # Using a non-empty wrong owner so the broker performs a deterministic
    # username comparison and denies access. An empty owner would trigger
    # auto-registration (covered by config_owner_auto_update.robot), not denial.
    Change Broker Configuration    owner    ${non_allowed_user}
    Log In With Remote User Through CLI: Local Password And Expect Failure    ${username}    ${local_password}

    # Scenario 3: explicit username → success
    Change allowed_users In Broker Configuration    ${username}
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From Terminal Session

    # Scenario 4: non-allowed username → failure
    Change allowed_users In Broker Configuration    ${non_allowed_user}
    Log In With Remote User Through CLI: Local Password And Expect Failure    ${username}    ${local_password}

    # Scenario 5: ALL → success
    Change allowed_users In Broker Configuration    ALL
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From Terminal Session

    Close Focused Window
