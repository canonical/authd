*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    ${snapshot}
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}
${local_password}    qwer1234
${new_gid}    60500


*** Test Cases ***
Test authctl group set-gid
    [Documentation]    Test that authctl group set-gid changes the GID of a remote group
    ...    and updates the home directory ownership.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # No session termination needed here: unlike set-uid (which calls
    # proc.CheckUserBusy), set-gid does not check for running processes.

    ${group_name} =    SSH.Execute    id -gn ${username}
    Should Not Be Empty    ${group_name}

    ${home_dir} =    SSH.Execute    getent passwd ${username} | cut -d: -f6
    Should Not Be Empty    ${home_dir}
    SSH.Execute    sudo -u ${username} touch ${home_dir}/test-file

    ${output} =    SSH.Execute    sudo authctl group set-gid ${group_name} ${new_gid} 2>&1
    Should Contain    ${output}    GID of group '${group_name}' set to ${new_gid}.

    ${actual_gid} =    SSH.Execute    getent group ${group_name} | cut -d: -f3
    Should Be Equal As Strings    ${actual_gid}    ${new_gid}

    ${reverse_lookup} =    SSH.Execute    getent group ${new_gid} | cut -d: -f1
    Should Be Equal As Strings    ${reverse_lookup}    ${group_name}

    ${passwd_gid} =    SSH.Execute    getent passwd ${username} | cut -d: -f4
    Should Be Equal As Strings    ${passwd_gid}    ${new_gid}

    ${home_gid} =    SSH.Execute    stat -c %g ${home_dir}
    Should Be Equal As Strings    ${home_gid}    ${new_gid}
    ${file_gid} =    SSH.Execute    sudo stat -c %g ${home_dir}/test-file
    Should Be Equal As Strings    ${file_gid}    ${new_gid}

    # This test case tests a bug that was fixed in https://github.com/canonical/authd/pull/1422/
    # The bug caused the user record's primary GID to revert
    # to the user's UID upon login, while the group record kept the correct GID,
    # causing `getent passwd` and `getent group` to diverge.
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    ${post_login_gid} =    SSH.Execute    getent passwd ${username} | cut -d: -f4
    Should Be Equal As Strings    ${post_login_gid}    ${new_gid}
