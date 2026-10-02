*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${username}    %{E2E_USER}
${local_password}    qwer1234
${first_home_base_dir}    /srv/authd-first-homes
${second_home_base_dir}    /srv/authd-second-homes


*** Test Cases ***
Test login keeps existing home directory after changing home base dir
    [Documentation]    Changing ``home_base_dir`` after the first login must not
    ...    move the user's existing home directory.
    ...
    ...    authd stores the home path in its user database when it creates the
    ...    account. Recalculating the path from the current config could leave the
    ...    user's data behind, so later logins use the saved path.
    ...
    ...    The test sets the first ``home_base_dir``, registers the user and checks
    ...    the path, owner and shell. It then points the setting at a second
    ...    directory and confirms that a later login still uses the first one.

    SSH.Execute    mkdir -p ${first_home_base_dir} ${second_home_base_dir}
    Change Broker Configuration    home_base_dir    ${first_home_base_dir}

    # Log in with local user.
    Log In

    # First remote login should create the home directory under the configured base path.
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    Check If User Was Added Properly    ${username}
    ${passwd_entry} =    SSH.Execute    getent passwd ${username}
    Should Contain    ${passwd_entry}    ${first_home_base_dir}/
    Should Contain    ${passwd_entry}    /bin/bash
    Check Home Directory    ${username}    ${first_home_base_dir}
    Log Out From Terminal Session
    Close Focused Window

    # Change the config after the user already exists.
    Change Broker Configuration    home_base_dir    ${second_home_base_dir}

    # Second login should still use the original home directory from the database.
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    ${passwd_entry} =    SSH.Execute    getent passwd ${username}
    Should Contain    ${passwd_entry}    ${first_home_base_dir}/
    Should Contain    ${passwd_entry}    /bin/bash
    Check Home Directory    ${username}    ${first_home_base_dir}
