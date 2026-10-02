*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Setup    utils.Test Setup    snapshot=${snapshot}
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}
${local_password}    qwer1234
${sudo_output_file}    /tmp/authd-pam-tty-background-sudo.out


*** Test Cases ***
Test sudo authentication when run in the background
    [Documentation]    Regresses authd issue #1566. Verify a backgrounded sudo
    ...    process can read its password from stdin when its controlling TTY
    ...    cannot be switched to raw mode.
    ...
    ...    The authd PAM client puts the terminal into raw mode to draw its prompt.
    ...    A background process is not in the terminal's foreground process group, so
    ...    that fails, and authd used to abort the conversation instead of falling
    ...    back to plain reads from stdin. Scripts that pipe a password into ``sudo``
    ...    stopped working for authd users as a result.
    ...
    ...    The remote user is registered through the device code flow, added to the
    ...    sudo group, and a fresh ``su`` session is opened so that sudo runs as them
    ...    on a real TTY. ``sudo -S ... &`` then reads the password from a here-string
    ...    while in the background, and the test asserts over SSH that the redirected
    ...    ``id`` output reports uid=0.
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
    Hid.Type String    bash -c 'sudo -S <<< "${local_password}" id > ${sudo_output_file} 2>&1 &' && echo Y21kLWZpbmlzaGVkCg== | base64 -d
    Hid.Keys Combo    Return
    # Wait for the shell to accept the command, then poll until sudo completes.
    Match Text    cmd-finished    30

    # Confirm the background command ran id as root.
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute
    ...    grep -Fq 'uid=0(root)' ${sudo_output_file}

    Log Out From su Session
    Close Focused Window
