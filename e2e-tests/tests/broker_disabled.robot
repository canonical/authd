*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}


*** Test Cases ***
Test that disabling broker prevents remote logins
    [Documentation]    Verify that disabling the broker blocks remote logins without
    ...    locking local users out of the machine.
    ...
    ...    The broker snap is stopped and its authd configuration file is removed, so
    ...    authd is left with only the local broker. The authd PAM module returns
    ...    PAM_IGNORE and hands authentication to the next PAM module, pam_unix, which
    ...    shows the plain "Password:" prompt instead of a provider menu. The test
    ...    checks that prompt without submitting a remote password.
    ...
    ...    Checks performed (in order):
    ...      1. a remote user typed into GDM sees the local broker's plain password
    ...         prompt
    ...      2. the local user can still log in through GDM
    ...      3. the local user can still become root with sudo
    ...      4. a remote login through ``machinectl login`` shows the local broker's
    ...         plain password prompt as well

    # Disable broker
    Disable Broker And Purge Config

    # Check that remote user is redirected to local broker when trying to log in through GDM
    Start Log In With Remote User Through GDM    ${username}
    Check That User Is Redirected To Local Broker
    Escape Back to GDM Login Screen

    # Check that local user can still log in
    Log In

    # Ensure local sudo user can still log in
    Open Terminal
    Enter Sudo Mode In Terminal
    Close Terminal In Sudo Mode

    # Check that remote user cannot log in
    Open Terminal
    Try Log In With Remote User    ${username}
    Check That User Is Redirected To Local Broker
