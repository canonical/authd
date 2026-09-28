*** Settings ***
Documentation       authd test resources

Resource            kvm.resource
Library             ./Journal.py    AS    Journal
Library             ./StringUtils.py    AS    StringUtils
Library             ./VideoLogger.py    AS    VideoLogger
Library             ./VNCRecorder.py   AS    VNCRecorder
Library             ./Snapshot.py    AS    Snapshot
Library             ./SSH.py    AS    SSH
Library             Process
Library             OperatingSystem
Library             String


*** Variables ***
${SUITE_OUTPUT_DIR}    ${OUTPUT_DIR}/${SUITE_NAME.rsplit('.')[-1]}
# Save yarf debug screenshots as files in the suite output directory so that
# they are referenced by relative URLs in log.html instead of being embedded
# as base64. Set to an empty string to fall back to inline base64 embedding.
${YARF_IMAGE_DIR}    ${SUITE_OUTPUT_DIR}/screenshots
${OCR_CONFIDENCE_THRESHOLD}    94
${OCR_SIMILARITY_THRESHOLD}    92


*** Keywords ***
Log In
    Log In With Password    ubuntu


Log In With Password
    [Arguments]    ${password}
    Wait Until GDM Login Screen Ready
    Select User
    Hid.Type String    ${password}
    Hid.Keys Combo    Return
    Wait Until Desktop Ready


Log In And Set Password
    [Arguments]    ${new_password}
    Wait Until GDM Login Screen Ready
    Select User
    Hid.Type String    ubuntu
    Hid.Keys Combo    Return

    Match Text    You are required to change your password immediately    30
    Match Text    Changing password   30
    Sleep   2
    Hid.Type String    ubuntu
    Hid.Keys Combo    Return

    Match Text    New password    30    confidence=88
    Hid.Type String    ${new_password}
    Hid.Keys Combo    Return

    Match Text    Retype new password    30    confidence=88
    Hid.Type String    ${new_password}
    Hid.Keys Combo    Return

    Wait Until Desktop Ready


Select User
    Wait Until Keyword Succeeds    120 sec    1 sec
    ...    Try Select User


Try Select User
    Hid.Keys Combo    Return
    # The blinking text cursor can fuse with the field's placeholder text,
    # making OCR read it as e.g. "IPassword", which lowers confidence below
    # the suite's global threshold.
    Match Text    Password    confidence=88


Log Out
    SSH.Execute    loginctl terminate-seat seat0


Lock Screen
    ${session_id} =    SSH.Execute    loginctl show-seat seat0 -p ActiveSession --value
    Should Not Be Empty    ${session_id}
    SSH.Execute    loginctl lock-session ${session_id}
    Wait Until Keyword Succeeds    30s    1s    Check Session Is Locked    ${session_id}


Check Session Is Locked
    [Arguments]    ${session_id}
    ${locked} =    SSH.Execute    loginctl show-session ${session_id} -p LockedHint --value
    Should Be Equal    ${locked}    yes


Unlock Screen With Password
    [Arguments]    ${password}
    # Locking the session can leave the display asleep until it receives input.
    Hid.Keys Combo    Return
    # Use a lower confidence threshold because the blinking text cursor can
    # merge with the prompt and lower OCR confidence
    Match Text    Enter your password    confidence=88
    Hid.Type String    ${password}
    Hid.Keys Combo    Return
    Wait Until Desktop Ready


Wait Until GDM Login Screen Ready
    Match Text    Not listed    180
    # Sometimes the "Not listed" is shown for a brief moment before the other
    # list items of the login screen are displayed, which resulted in the wrong
    # list item being clicked in the next step. To avoid that, we sleep for
    # 2 seconds to ensure the login screen is fully loaded before proceeding.
    BuiltIn.Sleep    2


Wait Until Desktop Ready
    Wait Until Keyword Succeeds    1 min    1 sec
    ...    Try Desktop Ready
    # Before 26.04, GNOME Shell sometimes starts with the Activities overview
    # opened, see https://bugs.launchpad.net/ubuntu/+source/gnome-shell/+bug/2145704.
    # That causes the next steps to fail, so we need to ensure the overview is
    # closed before proceeding.
    ${matches}=    Find Text    Type to search
    IF    ${matches}
        Log    Activities Overview is open, closing it
        Close Activities Overview
    END

Close Activities Overview
    WHILE    True
        Hid.Keys Combo    Escape
        BuiltIn.Sleep    1
        ${matches}=    Find Text    Type to search
        IF    not ${matches}
            BREAK
        END
    END


Try Desktop Ready
    Hid.Move Pointer To Proportional    1    1
    BuiltIn.Sleep    1
    Hid.Move Pointer To Proportional    0    1
    Match Text    Show Apps    5


Open Terminal
    IF    '%{RELEASE}' == 'noble'
        Launch App    org.gnome.Terminal.desktop
    ELSE
        Launch App    org.gnome.Ptyxis.desktop
    END
    Hid.Move Pointer To Proportional    1    1
    Match Text    @ubuntu:~$    120
    Hid.Keys Combo    F11
    Hid.Move Pointer To Proportional    1    1
    Match Text    @ubuntu:~$    120


Open Terminal In Sudo Mode
    Open Terminal
    Enter Sudo Mode In Terminal


Enter Sudo Mode In Terminal
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Hid.Type String    sudo -s
    Hid.Keys Combo    Return
    Match Text    root@ubuntu    120
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Match Text    root@ubuntu    120


Close Focused Window
    Hid.Keys Combo    Alt_L   F4


Close Terminal In Sudo Mode
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Match Text    root@ubuntu    120
    Hid.Type String    exit
    Hid.Keys Combo    Return
    Close Focused Window


Clear Terminal
    Hid.Type String    clear
    Hid.Keys Combo    Return


Log Out From Terminal Session
    Match Text    @ubuntu    120
    # We are in a machinectl session, so we need to ^] thrice to exit properly
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]


Log Out From su Session
    Match Text    @ubuntu    120
    Hid.Type String    exit
    Hid.Keys Combo    Return
    Match Text    @ubuntu:~$    30


Log Out From SSH Session
    Hid.Type String    exit
    Hid.Keys Combo    Return
    Match Text    ubuntu@ubuntu    120


Run Command In Terminal
    [Arguments]    ${command}    ${timeout}=${600}
    Open Terminal
    # Y21kLWZpbmlzaGVkCg== is the base64 encoding of "cmd-finished\n"
    # We use it to avoid that the `Match Text` below matches the command itself,
    # before the first clear is executed.
    Hid.Type String   clear && ${command} && echo Y21kLWZpbmlzaGVkCg== | base64 -d
    Hid.Keys Combo    Return
    Match Text    cmd-finished    ${timeout}
    Close Focused Window


Left Button Click
    BuiltIn.Sleep    0.5
    Click LEFT Button
    BuiltIn.Sleep    0.5
    Hid.Move Pointer To Proportional    1    1


# Run Command uses the Alt+F2 shortcut to open the "Run a Command" dialog and
# execute the given command.
# Consider using SSH.Execute As Current User instead, which is more reliable and
# faster because it doesn't use simulated keyboard input.
Run Command
    [Arguments]    ${command}
    Hid.Keys Combo    Alt_L    F2
    Match Text    Run a Command    15
    Hid.Type String    ${command}
    Wait Until Keyword Succeeds    5s    1s    Submit Run Command Dialog

Submit Run Command Dialog
    Hid.Keys Combo    Return
    # Ensure the dialog is closed, because sometimes the Return keypress does
    # not trigger the command execution and the dialog remains open.
    ${matches}=    Find Text    Run a Command
    Should Be Empty    ${matches}


Launch App
    [Arguments]    ${app-id}
    SSH.Execute As Current User    nohup gtk-launch ${app-id} </dev/null >/dev/null 2>&1 &


Cancel Operation
    Hid.Keys Combo    Control_L    c
    Match Text    @ubuntu    15


Update Authd
    ${apt_source}=    Get Environment Variable    APT_SOURCE    ${EMPTY}
    ${authd_apt_source}=    Get Environment Variable    AUTHD_APT_SOURCE    ${EMPTY}
    ${authd_deb}=    Get Environment Variable    AUTHD_DEB    ${EMPTY}
    IF    $apt_source == ''
        ${apt_source}=    Set Variable    ppa:ubuntu-enterprise-desktop/authd-edge
    END

    Add APT Source    ${apt_source}
    IF    $authd_apt_source != '' and $authd_apt_source != $apt_source
        Add APT Source    ${authd_apt_source}
    END

    ${authd_policy_source}=    Set Variable If    $authd_apt_source != ''    ${authd_apt_source}    ${apt_source}
    Configure APT Policy    ${apt_source}    ${authd_policy_source}

    SSH.Execute    apt-get update    timeout=300
    # authd constrains gnome-shell with a compatibility rule. Explicitly
    # install it from the selected source because the base image's archive
    # version may not satisfy authd's rule.
    SSH.Execute    apt-get install -y gnome-shell    timeout=120
    IF    $authd_deb != ''
        SSH.Copy To VM    ${authd_deb}    /home/ubuntu/authd-under-test.deb
        SSH.Execute    apt-mark unhold authd || true
        SSH.Execute    apt-get install -y --allow-downgrades /home/ubuntu/authd-under-test.deb    timeout=120
        Configure Local Authd Policy
        Verify Local Authd
    END
    SSH.Execute    apt-get full-upgrade -y    timeout=600

    IF    $authd_deb != ''
        Verify Local Authd
    ELSE
        SSH.Execute    apt-get install -y --allow-downgrades authd    timeout=120
        Verify Authd Source    ${authd_policy_source}
    END


Add APT Source
    [Arguments]    ${source}
    IF    '${source}'.startswith('ppa:')
        SSH.Execute    add-apt-repository -y -n ppa:${source[4:]}    timeout=300
    ELSE
        SSH.Execute
        ...    add-apt-repository -y -n -S 'deb http://archive.ubuntu.com/ubuntu/ ${source} main restricted universe multiverse'
    END


Get APT Pin
    [Arguments]    ${source}
    IF    '${source}'.startswith('ppa:')
        ${ppa}=    Get Substring    ${source}    4
        ${ppa}=    Replace String    ${ppa}    /    -
        RETURN    o=LP-PPA-${ppa}
    ELSE
        RETURN    a=${source}
    END


Get APT Source Reference
    [Arguments]    ${source}
    IF    '${source}'.startswith('ppa:')
        ${ppa}=    Get Substring    ${source}    4
        RETURN    /${ppa}/ubuntu
    ELSE
        RETURN    ${source}/
    END


Configure APT Policy
    [Arguments]    ${system_source}    ${authd_source}
    ${system_pin}=    Get APT Pin    ${system_source}
    ${authd_pin}=    Get APT Pin    ${authd_source}
    SSH.Execute
    ...    mkdir -p /etc/apt/preferences.d && printf '%s\n' 'Package: *' 'Pin: release ${system_pin}' 'Pin-Priority: 990' > /etc/apt/preferences.d/99-e2e-system-source
    SSH.Execute
    ...    printf '%s\n' 'Package: authd' 'Pin: release ${authd_pin}' 'Pin-Priority: 1001' > /etc/apt/preferences.d/99-e2e-authd-source
    IF    $system_source != $authd_source
        SSH.Execute
        ...    printf '%s\n' 'Package: *' 'Pin: release ${authd_pin}' 'Pin-Priority: 100' > /etc/apt/preferences.d/98-e2e-authd-source-fallback
    ELSE
        SSH.Execute    rm -f /etc/apt/preferences.d/98-e2e-authd-source-fallback
    END


Configure Local Authd Policy
    ${authd_version}=    SSH.Execute    dpkg-query -W -f='\${Version}' authd
    SSH.Execute
    ...    printf '%s\n' 'Package: authd' 'Pin: version ${authd_version}' 'Pin-Priority: 1001' > /etc/apt/preferences.d/99-e2e-authd-source
    SSH.Execute    apt-mark hold authd


Verify Authd Source
    [Arguments]    ${source}
    ${source_reference}=    Get APT Source Reference    ${source}
    ${installed_version}=    SSH.Execute    dpkg-query -W -f='\${Version}' authd
    ${candidate_version}=    SSH.Execute    apt-cache policy authd | awk '/Candidate:/ { print $2; exit }'
    Should Be Equal    ${candidate_version}    ${installed_version}
    ${policy}=    SSH.Execute    apt-cache policy authd
    Should Contain    ${policy}    ${source_reference}


Verify Local Authd
    ${installed_version}=    SSH.Execute    dpkg-query -W -f='\${Version}' authd
    Should Not Be Empty    ${installed_version}
    ${held_packages}=    SSH.Execute    apt-mark showhold
    Should Contain    ${held_packages}    authd


Sync System Time
    ${output} =    SSH.Execute    nm-online -q && systemctl restart systemd-timesyncd
    Log    ${output}    level=DEBUG
    ${output} =   SSH.Execute    timedatectl show -p NTPSynchronized | grep -q NTPSynchronized=yes
    Log    ${output}    level=DEBUG


Wait Until System Time Synced
    Wait Until Keyword Succeeds    1 min    1 sec
    ...    Sync System Time


Try machinectl login Prompt
    VAR    ${tries}    3
    WHILE    ${tries} > 0
        Hid.Type String    machinectl login
        Hid.Keys Combo    Return
        ${match} =     Run Keyword And Return Status
        ...    Match Text    ubuntu login:    120
        IF   '${match}' == 'True'
            BREAK
        END
        Hid.Keys Combo    Control_L    ]
        Hid.Keys Combo    Control_L    ]
        Hid.Keys Combo    Control_L    ]
        ${tries} =    Evaluate    ${tries} - 1
    END

Terminal Password Change
    [arguments]    ${new_password}
    Hid.Type String    passwd
    Hid.Keys Combo    Return
    Match Text    Current password:    5
    Hid.Type String    ubuntu
    Hid.Keys Combo    Return
    Match Text    New password:    5
    Hid.Type String    ${new_password}
    Hid.Keys Combo    Return
    Match Text    Retype new password:    5
    Hid.Type String    ${new_password}
    Hid.Keys Combo    Return
    Match Text    passwd: password updated successfully    30
    Clear Terminal

Terminal Password Change Error
    [arguments]    ${new_password}    ${expected_error}
    Hid.Type String    passwd
    Hid.Keys Combo    Return
    Match Text    Current password:    5
    Hid.Type String    ubuntu
    Hid.Keys Combo    Return
    Match Text    New password:    5
    Hid.Type String    ${new_password}
    Hid.Keys Combo    Return
    Match Text    ${expected_error}
    Hid.Keys Combo    Control_L    d
    Match Text    password unchanged     10
    Match Text    ubuntu@ubuntu:~$    10
    Clear Terminal

Force Password Change
    Hid.Type String    passwd -e ubuntu
    Hid.Keys Combo    Return
    Match Text    passwd: password changed.    30


Escape Back to GDM Login Screen
    Hid.Keys Combo    Escape
    Match Text    Not listed    120


Log Installed Package Information
    [Arguments]    ${snapshot}
    ${broker_version}=    SSH.Execute    snap list %{BROKER} | awk -v broker='%{BROKER}' '$1 == broker {if ($3 ~ /^x[0-9]+$/) printf "%s (local, rev %s)\\n", $2, $3; else printf "%s (%s, rev %s)\\n", $2, $4, $3; exit}'
    ${authd_dependencies}=    SSH.Execute
    ...    { printf '%s\\n' authd gnome-shell; apt-cache depends authd | awk '/^[[:space:]|]+(Pre-Depends|Depends|Recommends|Breaks): / {print $2}'; } | sort -u | while read -r package; do if ! dpkg-query --show "$package" 2>/dev/null; then printf '%s (not installed)\\n' "$package"; fi; done
    ${authd_apt_policy}=    SSH.Execute    apt-cache policy authd
    ${gnome_shell_apt_policy}=    SSH.Execute    apt-cache policy gnome-shell yaru-theme-gnome-shell

    # Keep stable package versions separate so migration setup does not
    # overwrite the under-test versions stored on the root suite.
    IF    '${snapshot}' == '%{BROKER}-stable-installed'
        Set Suite Metadata    authd and Dependencies (Stable)    ${authd_dependencies}    top=True
        Set Suite Metadata    Broker Version (Stable)    ${broker_version}    top=True
    ELSE
        Set Suite Metadata    authd and Dependencies    ${authd_dependencies}    top=True
        Set Suite Metadata    Broker Version    ${broker_version}    top=True
    END

    Log    authd apt policy:\n${authd_apt_policy}
    Log    gnome-shell apt policy:\n${gnome_shell_apt_policy}


### Setup ###

Restore Snapshot
    [Arguments]    ${snapshot}
    ${snapshot_override}=    Get Environment Variable    E2E_TEST_SNAPSHOT    ${EMPTY}
    IF    $snapshot_override
        ${snapshot}=    Set Variable    ${snapshot_override}
    END
    Snapshot.Restore    ${snapshot}
    Wait Until System Time Synced
    RETURN    ${snapshot}

Test Setup
    [Arguments]    ${snapshot}
    [Documentation]    Restores the suite snapshot, or the snapshot named by
    ...    E2E_TEST_SNAPSHOT when that environment variable is non-empty.
    Set Suite Metadata    Ubuntu Version   %{RELEASE}    top=True
    Set Suite Metadata    Broker    %{BROKER}    top=True
    ${snapshot}=    Restore Snapshot    ${snapshot}
    Log Installed Package Information    ${snapshot}
    Journal.Start Receiving Journal
    VNCRecorder.Start Recording

### Teardown ###

Test Teardown
    VNCRecorder.Stop Recording
    Journal.Stop Receiving Journal
    VideoLogger.Log videos
    Journal.Log Journal
