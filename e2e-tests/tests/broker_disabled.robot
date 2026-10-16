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
Test remote login falls back to local PAM when broker is unavailable
    [Documentation]    With the broker unavailable, authd passes remote
    ...    authentication to local PAM. Local users can still log in.
    ...
    ...    The test stops the broker snap and removes its authd config. With only
    ...    the local broker available, authd returns ``PAM_IGNORE``. The next
    ...    PAM module, ``pam_unix``, shows a plain ``Password:`` prompt. The test
    ...    checks that prompt but does not submit a password.
    ...
    ...    The test checks these cases in order:
    ...    - a remote GDM login shows the ``pam_unix`` password prompt
    ...    - the local user can still log in through GDM
    ...    - the local user can still become root with ``sudo``
    ...    - a remote ``machinectl login`` shows the ``pam_unix`` password prompt

    # Disable broker
    Disable Broker And Purge Config

    # Check that GDM falls back to the pam_unix prompt.
    Start Log In With Remote User Through GDM    ${username}
    Check That PAM Unix Prompt Is Shown
    Escape Back to GDM Login Screen

    # Check that local user can still log in
    Log In

    # Ensure local sudo user can still log in
    Open Terminal
    Enter Sudo Mode In Terminal
    Close Terminal In Sudo Mode

    # Check that machinectl falls back to the pam_unix prompt.
    Open Terminal
    Try Log In With Remote User    ${username}
    Check That PAM Unix Prompt Is Shown
