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
    [Documentation]    Verify that the first login creates the home directory under
    ...    the configured ``home_base_dir`` and that changing the setting later does
    ...    not move an existing home directory.
    ...
    ...    The home directory path is stored in the authd database when the user
    ...    record is created. Re-deriving it from the configuration on every login
    ...    would strand the user's data in the old location, so the stored value has
    ...    to win.
    ...
    ...    Steps:
    ...      1. point home_base_dir at the first directory, register the user with the
    ...         device code flow, and check the home directory was created under it,
    ...         owned by the user, with bash as the login shell
    ...      2. point home_base_dir at a second directory
    ...      3. log in again with the local password and check the home directory is
    ...         still the original one

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
