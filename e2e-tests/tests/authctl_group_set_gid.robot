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
${new_gid}           60500


*** Test Cases ***
Test authctl group set-gid
    [Documentation]    Changing a remote user's primary-group GID updates matching
    ...    ownership in that user's home directory.
    ...
    ...    Admins may need authd-managed group IDs to match IDs on a shared NFS
    ...    server. ``authctl group set-gid`` changes the GID and re-owns old-GID
    ...    files under the homes of users whose primary group changed. Files
    ...    elsewhere need manual updates.
    ...
    ...    The test registers a remote user and creates a file in the home. It checks
    ...    ownership only for that directory and file.
    ...
    ...    A later login guards against the primary-GID reset fixed in
    ...    https://github.com/canonical/authd/pull/1422/.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # No session termination needed here: unlike set-uid (which calls
    # proc.CheckUserBusy), set-gid does not check for running processes.

    ${group_name} =    Get User Primary Group    ${username}
    ${home_dir} =    Create File In User Home    ${username}    test-file
    Set Group GID    ${group_name}    ${new_gid}
    Check Group GID    ${group_name}    ${new_gid}
    Check User Primary GID    ${username}    ${new_gid}
    Check Home And Test File GID    ${home_dir}    ${new_gid}

    # This test case tests a bug that was fixed in https://github.com/canonical/authd/pull/1422/
    # The bug caused the user record's primary GID to revert
    # to the user's UID upon login, while the group record kept the correct GID,
    # causing `getent passwd` and `getent group` to diverge.
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check User Primary GID    ${username}    ${new_gid}


*** Keywords ***
Get User Primary Group
    [Arguments]    ${username}
    ${group_name} =    SSH.Execute    id -gn ${username}
    Should Not Be Empty    ${group_name}
    RETURN    ${group_name}

Set Group GID
    [Arguments]    ${group_name}    ${gid}
    ${output} =    SSH.Execute    authctl group set-gid ${group_name} ${gid} 2>&1
    Should Contain    ${output}    GID of group '${group_name}' set to ${gid}.

Check Group GID
    [Arguments]    ${group_name}    ${gid}
    ${actual_gid} =    SSH.Execute    getent group ${group_name} | cut -d: -f3
    Should Be Equal As Strings    ${actual_gid}    ${gid}

    ${reverse_lookup} =    SSH.Execute    getent group ${gid} | cut -d: -f1
    Should Be Equal As Strings    ${reverse_lookup}    ${group_name}

Check User Primary GID
    [Arguments]    ${username}    ${gid}
    ${passwd_gid} =    SSH.Execute    getent passwd ${username} | cut -d: -f4
    Should Be Equal As Strings    ${passwd_gid}    ${gid}

Check Home And Test File GID
    [Arguments]    ${home_dir}    ${gid}
    ${home_gid} =    SSH.Execute    stat -c %g ${home_dir}
    Should Be Equal As Strings    ${home_gid}    ${gid}
    ${file_gid} =    SSH.Execute    stat -c %g ${home_dir}/test-file
    Should Be Equal As Strings    ${file_gid}    ${gid}
