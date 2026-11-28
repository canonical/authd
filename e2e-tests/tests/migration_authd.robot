*** Settings ***
Resource         resources/utils.resource
Resource         resources/authd.resource
Resource         resources/broker.resource

# Test Tags    robot:exit-on-failure
Test Setup       utils.Test Setup    snapshot=%{BROKER}-stable-installed
Test Teardown    utils.Test Teardown


*** Variables ***
${username}          %{E2E_USER}
${local_password}    qwer1234


*** Test Cases ***
Test login after updating authd to the version under test
    [Documentation]    A user registered on stable authd can still log in after
    ...    authd is upgraded to the version under test.
    ...
    ...    The ``-stable-installed`` snapshot has stable authd and broker. A
    ...    device-code login registers the account and populates the broker cache.
    ...    The test checks setup and logs in locally, upgrades authd and supported
    ...    gnome-shell packages, then logs in again with the local password. That
    ...    login checks the account, cache, home path and ownership.

    # Log in with local user
    Log In

    # Log in with remote user with device code flow
    Open Terminal
    Log In With Remote User Through CLI: QR Code    ${username}    ${local_password}
    # Check remote user is properly added to the system
    Check If User Was Added Properly    ${username}
    Log Out From Terminal Session
    Close Focused Window

    # Log in with remote user with local password
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Log Out From su Session
    Close Focused Window

    Update Authd    skip_if_authd_stable_ppa_is_unavailable=${True}

    Log Authd Package Policies

    # Log in with remote user with local password after upgrading
    Open Terminal
    Log In With Remote User Through CLI: Local Password    ${username}    ${local_password}
    Check Home Directory    ${username}

Test authd is available after package reinstall
    [Documentation]    Reinstalling authd restarts the service and leaves both
    ...    units active.
    ...
    ...    Start with the version under test already running so the package hooks
    ...    run with a populated descriptor store, even when the stable baseline
    ...    does not have one (#2317).
    [Setup]    utils.Test Setup    snapshot=%{BROKER}-installed
    Check Authd Units Are Active
    ${descriptor_count} =    Get Authd Descriptor Count
    Should Be True    int($descriptor_count) > 0    authd has no stored descriptors before reinstall
    ${old_invocation} =    Get Authd Service Invocation ID
    Reinstall Installed Authd Package
    Check Authd Units Are Active
    ${new_invocation} =    Get Authd Service Invocation ID
    Should Not Be Equal    ${new_invocation}    ${old_invocation}    authd.service did not restart


*** Keywords ***
Check Authd Units Are Active
    SSH.Execute    systemctl is-active authd.socket && systemctl is-active authd.service

Get Authd Service Invocation ID
    ${id} =    SSH.Execute    systemctl show authd.service -p InvocationID --value
    RETURN    ${id}

Reinstall Installed Authd Package
    ${authd_deb} =    Get Environment Variable    AUTHD_DEB    ${EMPTY}
    ${directory} =    SSH.Execute    mktemp -d
    ${staged_deb} =    Set Variable    ${directory}/authd.deb
    ${installed_version} =    SSH.Execute    dpkg-query -W -f='\${Version}' authd
    IF    $authd_deb
        SSH.Copy To VM    ${authd_deb}    ${staged_deb}
    ELSE
        SSH.Execute
        ...    cd ${directory} && apt-get download "authd=${installed_version}" && mv authd_*.deb authd.deb
        ...    timeout=120
    END
    ${staged_version} =    SSH.Execute    dpkg-deb -f ${staged_deb} Version
    Should Be Equal    ${staged_version}    ${installed_version}
    SSH.Execute    dpkg --install ${staged_deb}    timeout=120
