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
${new_username}    renamed-%{E2E_USER}
${local_group}    authd-e2e-renamegroup


*** Test Cases ***
Test authctl user set-name
    [Documentation]    Test that authctl user set-name renames a remote user, its private
    ...    group and its local group memberships, while leaving the UID and the
    ...    home directory untouched, and that a later broker login keeps the new
    ...    name instead of restoring the one the identity provider reports.

    Log In

    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    ${old_uid} =    SSH.Execute    id -u ${username}
    Should Not Be Empty    ${old_uid}

    ${home_dir} =    SSH.Execute    getent passwd ${username} | cut -d: -f6
    Should Not Be Empty    ${home_dir}

    # authd names the private group after the user, so the rename has to update it too.
    ${old_group} =    SSH.Execute    id -gn ${username}
    Should Be Equal As Strings    ${old_group}    ${username}
    ${old_gid} =    SSH.Execute    id -g ${username}
    Should Not Be Empty    ${old_gid}

    # Put the user in a local group so that the rewrite of /etc/group is covered. This guards
    # against a regression where the rename updated the database but silently left every
    # /etc/group membership pointing at the old username.
    SSH.Execute    groupadd ${local_group}
    SSH.Execute    gpasswd -a ${username} ${local_group}
    ${members} =    SSH.Execute    getent group ${local_group} | cut -d: -f4
    Should Be Equal As Strings    ${members}    ${username}

    # Terminate the remote user's session so that proc.CheckUserBusy (which
    # rejects set-name when any process runs under that UID) does not block the
    # operation.  Use loginctl to tear down the session gracefully, then poll
    # until all processes have exited rather than relying on a hard sleep.
    SSH.Execute    loginctl terminate-user ${username} || true
    Wait Until Keyword Succeeds    30s    1s    SSH.Execute    test -z "$(pgrep -u ${username})"

    ${output} =    SSH.Execute    authctl user set-name ${username} ${new_username} 2>&1
    Should Contain    ${output}    User '${username}' renamed to '${new_username}'.
    Should Contain    ${output}    The user's private group was renamed to '${new_username}' as well.
    # The home directory keeps its old path, so the command has to say so.
    Should Contain    ${output}    still refers to the old username

    # The user resolves under the new name only.
    ${new_lookup} =    SSH.Execute    getent passwd ${new_username}
    Should Contain    ${new_lookup}    ${new_username}:x:
    ${old_lookup} =    SSH.Execute    getent passwd ${username} || true
    Should Be Empty    ${old_lookup}

    # Renaming must not reallocate the UID, otherwise the user would lose access to its files.
    ${new_uid} =    SSH.Execute    id -u ${new_username}
    Should Be Equal As Strings    ${new_uid}    ${old_uid}

    # The private group follows the user, so NSS reports the new name for it. Compare the GID
    # rather than the whole entry: authd leaves the password field of a group empty.
    ${new_group} =    SSH.Execute    id -gn ${new_username}
    Should Be Equal As Strings    ${new_group}    ${new_username}
    ${new_gid} =    SSH.Execute    getent group ${new_username} | cut -d: -f3
    Should Be Equal As Strings    ${new_gid}    ${old_gid}
    ${old_group_lookup} =    SSH.Execute    getent group ${username} || true
    Should Be Empty    ${old_group_lookup}

    # The local group membership must have been rewritten in /etc/group. The user is the only
    # member, so the member list has to be exactly the new name.
    ${members} =    SSH.Execute    getent group ${local_group} | cut -d: -f4
    Should Be Equal As Strings    ${members}    ${new_username}

    # The home directory is deliberately left in place, still owned by the user.
    ${new_home} =    SSH.Execute    getent passwd ${new_username} | cut -d: -f6
    Should Be Equal As Strings    ${new_home}    ${home_dir}
    ${home_uid} =    SSH.Execute    stat -c %u ${home_dir}
    Should Be Equal As Strings    ${home_uid}    ${old_uid}

    # The renamed account is still usable: NSS and PAM account management resolve it.
    ${sudo_uid} =    SSH.Execute As User    ${new_username}    id -u
    Should Be Equal As Strings    ${sudo_uid}    ${old_uid}

    # A broker login must not undo the rename. The broker keeps reporting the username the
    # identity provider has, so authd has to keep the locally set one instead of renaming the
    # account back.
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${new_username}    ${local_password}

    ${name_after_login} =    SSH.Execute    id -un ${old_uid}
    Should Be Equal As Strings    ${name_after_login}    ${new_username}
    ${old_lookup_after_login} =    SSH.Execute    getent passwd ${username} || true
    Should Be Empty    ${old_lookup_after_login}

    # The login rewrites the local group memberships authd manages, so check that it did not
    # bring the old username back into /etc/group either.
    ${members_after_login} =    SSH.Execute    getent group ${local_group} | cut -d: -f4
    Should Be Equal As Strings    ${members_after_login}    ${new_username}
