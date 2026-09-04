*** Settings ***
Documentation       Tests for the use_short_usernames authd configuration option.

Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Library         String

Test Setup    Test Setup
Test Teardown   utils.Test Teardown


*** Keywords ***
Test Setup
    utils.Test Setup    snapshot=%{BROKER}-installed
    Change Authd Configuration    use_short_usernames    true


Test Setup With SSH First Authentication Allowed
    Test Setup
    Change Broker Configuration    ssh_allowed_suffixes_first_auth    %{E2E_USER}


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login with short username through CLI
    [Documentation]    Verify that, with short usernames enabled, a user is created under the
    ...                shortened form of their username, is resolved through both forms, and can
    ...                then log in with either of them. Also verify that disabling the option again
    ...                renames the user back to their fully qualified username.

    ${short_username} =    Fetch From Left    ${username}    @

    # Log in with local user
    Log In

    # The first authentication has to use the fully qualified username, because authd has no way to
    # resolve the domain before it knows the user. The user is nevertheless created under the
    # shortened name, which is therefore the one shown in the shell prompt.
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}    ${short_username}
    Check If User Was Added Properly    ${short_username}
    Check Home Directory    ${short_username}
    Check User Is Resolved By Both Names    ${short_username}    ${username}
    Check User Private Group Follows The Short Username    ${short_username}
    Log Out From Terminal Session
    Close Focused Window

    # From now on the shortened username is enough to log in...
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${short_username}    ${local_password}
    Log Out From su Session

    # ...and the fully qualified one still works, since authd maps it back to the same user.
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}    ${short_username}
    Log Out From su Session

    # Disabling the option again must be as safe as enabling it: the shortened user is still
    # resolvable through their fully qualified name, so they can log in and be renamed back.
    Change Authd Configuration    use_short_usernames    false
    Log In With Remote User Through CLI: Local Password
    ...    ${username}    ${local_password}    ${short_username}    ${username}
    Log Out From su Session
    Wait Until Keyword Succeeds    30s    1s
    ...    Check User Is Resolved By The Full Username Only    ${short_username}    ${username}


Test login with short username through SSH
    [Documentation]    Verify that, with short usernames enabled, a user created through SSH with
    ...                their fully qualified username gets the shortened name for their session and
    ...                can log in again with it.

    [Setup]    Test Setup With SSH First Authentication Allowed

    ${short_username} =    Fetch From Left    ${username}    @

    # Log in with local user
    Log In

    # The first authentication has to use the fully qualified username. SSH keeps sending that name
    # to PAM, so the shortened name only comes from the passwd entry authd returns, which is what
    # the session and the shell prompt end up using.
    Open Terminal
    Log In With Remote User Through SSH: QR Code    ${username}    ${local_password}    ${short_username}
    Check If User Was Added Properly    ${short_username}
    Check Home Directory    ${short_username}
    Check User Is Resolved By Both Names    ${short_username}    ${username}
    Log Out From SSH Session
    Close Focused Window

    # The shortened username is enough to log in again.
    Open Terminal
    Log In With Remote User Through SSH: Local Password    ${short_username}    ${local_password}
