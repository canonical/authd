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
${new_uid}    60500


*** Test Cases ***
Test authctl user set-uid
    [Documentation]    Test that authctl user set-uid changes the UID of a remote user
    ...    and updates the home directory ownership.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    ${home_dir} =    SSH.Execute    getent passwd ${username} | cut -d: -f6
    Should Not Be Empty    ${home_dir}
    SSH.Execute    sudo -u ${username} touch ${home_dir}/test-file

    # Terminate the remote user's session so that proc.CheckUserBusy (which
    # rejects set-uid when any process runs under that UID) does not block the
    # operation.  Use loginctl to tear down the session gracefully, then poll
    # until all processes have exited rather than relying on a hard sleep.
    SSH.Execute    sudo loginctl terminate-user ${username} || true
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute    test -z "$(pgrep -u ${username})"

    ${output} =    SSH.Execute    sudo authctl user set-uid ${username} ${new_uid} 2>&1
    Should Contain    ${output}    UID of user '${username}' set to ${new_uid}.

    ${actual_uid} =    SSH.Execute    getent passwd ${username} | cut -d: -f3
    Should Be Equal As Strings    ${actual_uid}    ${new_uid}

    ${reverse_lookup} =    SSH.Execute    getent passwd ${new_uid} | cut -d: -f1
    Should Be Equal As Strings    ${reverse_lookup}    ${username}

    ${id_output} =    SSH.Execute    id ${username}
    Should Contain    ${id_output}    uid=${new_uid}

    ${home_uid} =    SSH.Execute    stat -c %u ${home_dir}
    Should Be Equal As Strings    ${home_uid}    ${new_uid}
    ${file_uid} =    SSH.Execute    sudo stat -c %u ${home_dir}/test-file
    Should Be Equal As Strings    ${file_uid}    ${new_uid}

    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    ${id_uid} =    SSH.Execute    id -u ${username}
    Should Be Equal As Strings    ${id_uid}    ${new_uid}
