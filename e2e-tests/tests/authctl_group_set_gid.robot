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
${new_gid}    60500


*** Test Cases ***
Test authctl group set-gid
    [Documentation]    Verify that ``authctl group set-gid`` changes the GID of a
    ...    remote user's primary group and re-owns the files that belonged to the
    ...    old GID.
    ...
    ...    Administrators need this to line up authd-managed groups with GIDs that
    ...    already exist elsewhere, for example on a shared NFS server. Changing the
    ...    GID is only safe if every place that stores it is updated at once.
    ...
    ...    The user is first registered through the device code flow so that a remote
    ...    group exists, and a file is created in the home directory so the recursive
    ...    re-ownership is covered as well.
    ...
    ...    Checks performed after the change:
    ...      1. authctl reports the new GID
    ...      2. ``getent group <name>`` returns the new GID
    ...      3. ``getent group <new gid>`` resolves back to the group name
    ...      4. the user's primary GID in ``getent passwd`` follows the change
    ...      5. the home directory and the file inside it are owned by the new GID
    ...      6. logging in again keeps the new GID
    ...
    ...    The last check guards the regression fixed in
    ...    https://github.com/canonical/authd/pull/1422/.

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
    SSH.Execute As User    ${username}    touch ${home_dir}/test-file

    ${output} =    SSH.Execute    authctl group set-gid ${group_name} ${new_gid} 2>&1
    Should Contain    ${output}    GID of group '${group_name}' set to ${new_gid}.

    ${actual_gid} =    SSH.Execute    getent group ${group_name} | cut -d: -f3
    Should Be Equal As Strings    ${actual_gid}    ${new_gid}

    ${reverse_lookup} =    SSH.Execute    getent group ${new_gid} | cut -d: -f1
    Should Be Equal As Strings    ${reverse_lookup}    ${group_name}

    ${passwd_gid} =    SSH.Execute    getent passwd ${username} | cut -d: -f4
    Should Be Equal As Strings    ${passwd_gid}    ${new_gid}

    ${home_gid} =    SSH.Execute    stat -c %g ${home_dir}
    Should Be Equal As Strings    ${home_gid}    ${new_gid}
    ${file_gid} =    SSH.Execute    stat -c %g ${home_dir}/test-file
    Should Be Equal As Strings    ${file_gid}    ${new_gid}

    # This test case tests a bug that was fixed in https://github.com/canonical/authd/pull/1422/
    # The bug caused the user record's primary GID to revert
    # to the user's UID upon login, while the group record kept the correct GID,
    # causing `getent passwd` and `getent group` to diverge.
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    ${post_login_gid} =    SSH.Execute    getent passwd ${username} | cut -d: -f4
    Should Be Equal As Strings    ${post_login_gid}    ${new_gid}
