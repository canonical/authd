*** Settings ***
Resource         resources/utils.resource
Resource         resources/authd.resource
Resource         resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup       utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown    utils.Test Teardown


*** Variables ***
${username}          %{E2E_USER}
${local_password}    qwer1234
${new_uid}           60500


*** Test Cases ***
Test authctl user set-uid
    [Documentation]    A remote user's UID change re-owns matching files in the
    ...    home directory.
    ...
    ...    Admins may need authd-managed UIDs to match IDs on shared NFS.
    ...    ``authctl user set-uid`` changes ownership only inside the user's home;
    ...    files elsewhere need manual updates.
    ...
    ...    The test registers the user, creates a home file and ends the session
    ...    before changing the UID. The command refuses to run while processes use
    ...    the old UID.
    ...
    ...    A later local-password login verifies that the new UID persists.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    ${home_dir} =    Create File In User Home    ${username}    test-file
    Terminate User Sessions    ${username}
    Set User UID    ${username}    ${new_uid}
    Check User UID    ${username}    ${new_uid}
    Check Home And Test File UID    ${home_dir}    ${new_uid}

    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check User UID After Login    ${username}    ${new_uid}


*** Keywords ***
Terminate User Sessions
    [Arguments]    ${username}
    # Terminate the remote user's session so that proc.CheckUserBusy (which
    # rejects set-uid when any process runs under that UID) does not block the
    # operation. Use loginctl to tear down the session gracefully, then poll
    # until all processes have exited rather than relying on a hard sleep.
    SSH.Execute    loginctl terminate-user ${username} || true
    Wait Until Keyword Succeeds    30s    1s    Check User Has No Running Processes    ${username}

Check User Has No Running Processes
    [Arguments]    ${username}
    SSH.Execute    test -z "$(pgrep -u ${username})"

Set User UID
    [Arguments]    ${username}    ${uid}
    ${output} =    SSH.Execute    authctl user set-uid ${username} ${uid} 2>&1
    Should Contain    ${output}    UID of user '${username}' set to ${uid}.

Check User UID
    [Arguments]    ${username}    ${uid}
    ${actual_uid} =    SSH.Execute    getent passwd ${username} | cut -d: -f3
    Should Be Equal As Strings    ${actual_uid}    ${uid}

    ${reverse_lookup} =    SSH.Execute    getent passwd ${uid} | cut -d: -f1
    Should Be Equal As Strings    ${reverse_lookup}    ${username}

    ${id_output} =    SSH.Execute    id ${username}
    Should Contain    ${id_output}    uid=${uid}

Check Home And Test File UID
    [Arguments]    ${home_dir}    ${uid}
    ${home_uid} =    SSH.Execute    stat -c %u ${home_dir}
    Should Be Equal As Strings    ${home_uid}    ${uid}
    ${file_uid} =    SSH.Execute    stat -c %u ${home_dir}/test-file
    Should Be Equal As Strings    ${file_uid}    ${uid}

Check User UID After Login
    [Arguments]    ${username}    ${uid}
    ${id_uid} =    SSH.Execute    id -u ${username}
    Should Be Equal As Strings    ${id_uid}    ${uid}
