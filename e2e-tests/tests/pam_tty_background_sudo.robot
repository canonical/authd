*** Settings ***
Resource            resources/utils.resource
Resource            resources/authd.resource
Resource            resources/broker.resource

Test Setup          utils.Test Setup    snapshot=${snapshot}
Test Teardown       utils.Test Teardown


*** Variables ***
${snapshot}             %{BROKER}-installed
${username}             %{E2E_USER}
${local_password}       qwer1234
${sudo_output_file}     /tmp/authd-pam-tty-background-sudo.out


*** Test Cases ***
Test sudo authentication when run in the background
    [Documentation]    Backgrounded ``sudo`` reads its password from stdin when
    ...    its controlling TTY cannot switch to raw mode.
    ...
    ...    The PAM client puts the terminal in raw mode to draw its prompt.
    ...    Background processes are not in the foreground group, so the switch
    ...    fails. authd used to abort instead of reading stdin, breaking scripts
    ...    that piped passwords into ``sudo``. This was the regression in authd
    ...    issue #1566.
    ...
    ...    The test registers the remote user, adds them to sudo, and opens a fresh
    ...    ``su`` session on a real TTY. It runs ``sudo -S ... &`` with a here-string
    ...    and checks over SSH that redirected ``id`` output reports uid=0.
    Log In
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}

    # Add the authd user to the sudo group
    SSH.Execute    sudo usermod -aG sudo '${username}'

    # Start a fresh su session so sudo runs as the remote user on a TTY.
    Log Out From Terminal Session
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}

    # Remove any result from an earlier run before starting sudo in the background.
    SSH.Execute    rm -f ${sudo_output_file}
    # Reproduce the failure: sudo reads its password from stdin while running in
    # the background. Save id's output so the test can verify sudo became root.
    Hid.Type String
    ...    bash -c 'sudo -S <<< "${local_password}" id > ${sudo_output_file} 2>&1 &' && echo Y21kLWZpbmlzaGVkCg== | base64 -d
    Hid.Keys Combo    Return
    # Wait for the shell to accept the command, then poll until sudo completes.
    Match Text    cmd-finished    30

    # Confirm the background command ran id as root.
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute
    ...    grep -Fq 'uid=0(root)' ${sudo_output_file}

    Log Out From su Session
    Close Focused Window
