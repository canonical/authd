*** Settings ***
Documentation       authd test resources

Resource            ../kvm.resource
Library             ./StringUtils.py    AS    StringUtils
Library             ./VideoLogger.py    AS    VideoLogger
Library             ./Snapshot.py    AS    Snapshot
Library             ./SSH.py    AS    SSH
Library             Process
Library             OperatingSystem


*** Keywords ***
Log In
    Set OCR Confidence Threshold    0.94
    Set OCR Coincidence Threshold    92
    Match Text    Not listed    300
    Hid.Keys Combo    Return
    Match Text    Password
    Hid.Type String    ubuntu
    Hid.Keys Combo    Return
    Wait Until Desktop Ready


Log Out
    Run Command    gnome-session-quit --logout --no-prompt


Wait Until Desktop Ready
    Wait Until Keyword Succeeds    1 min    1 sec
    ...    Try Desktop Ready


Try Desktop Ready
    Hid.Move Pointer To Proportional    1    1
    BuiltIn.Sleep    1
    Hid.Move Pointer To Proportional    0    1
    Match Text    Show Apps    10


Open Terminal
    Run Command    x-terminal-emulator
    Hid.Move Pointer To Proportional    1    1
    Match Text    @ubuntu:~$    30
    Hid.Keys Combo    F11
    Hid.Move Pointer To Proportional    1    1
    Match Text    @ubuntu:~$    30


Open Terminal In Sudo Mode
    Open Terminal
    Enter Sudo Mode In Terminal


Enter Sudo Mode In Terminal
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Hid.Type String    sudo -s
    Hid.Keys Combo    Return
    Match Text    root@ubuntu    30
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Match Text    root@ubuntu    30


Close Focused Window
    Hid.Keys Combo    Alt_L   F4


Close Terminal In Sudo Mode
    Hid.Type String    clear
    Hid.Keys Combo    Return
    Match Text    root@ubuntu    30
    Hid.Type String    exit
    Hid.Keys Combo    Return
    Close Focused Window


Log Out From Terminal Session
    Match Text    @ubuntu    30
    # We are in a machinectl session, so we need to ^] thrice to exit properly
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]


Log Out From SSH Session
    Hid.Type String    exit
    Hid.Keys Combo    Return
    Match Text    ubuntu@ubuntu    30


Run Command In Terminal
    [Arguments]    ${command}
    Open Terminal
    Run Command In Focused Terminal    ${command}
    Close Focused Window


Run Command In Focused Terminal
    [Arguments]    ${command}    ${timeout}=${600}
    # Y21kLWZpbmlzaGVkCg== is the base64 encoding of "cmd-finished\n"
    # We use it to avoid that the `Match Text` below matches the command itself,
    # before the first clear is executed.
    Hid.Type String   clear && ${command} && echo Y21kLWZpbmlzaGVkCg== | base64 -d
    Hid.Keys Combo    Return
    Match Text    cmd-finished    ${timeout}


Left Button Click
    BuiltIn.Sleep    0.5
    Click LEFT Button
    BuiltIn.Sleep    0.5
    Hid.Move Pointer To Proportional    1    1


Run Command
    [Arguments]    ${command}
    Hid.Keys Combo    Alt_L    F2
    Match Text    Run a Command    15
    Hid.Type String    ${command}
    Hid.Keys Combo   Return


Start Application
    [Arguments]    ${application}
    Run Command    ${application}


Cancel Operation
    Match Text    @ubuntu    15
    Hid.Keys Combo    Control_L    C


Update And Upgrade Packages
    Open Terminal In Sudo Mode
    Run Command In Focused Terminal    apt-get update
    Run Command In Focused Terminal    apt-get upgrade -y    timeout=1200
    Close Terminal In Sudo Mode

Sync System Time
    SSH.Execute    sudo systemctl restart chronyd && sudo chronyc waitsync

Wait Until System Time Synced
    Wait Until Keyword Succeeds    1 min    1 sec
    ...    Sync System Time

### Setup ###

Restore Snapshot
    [Arguments]    ${snapshot_name}
    Snapshot.Restore    ${snapshot_name}
    Wait Until System Time Synced


### Teardown ###

Log Videos On Error
    Run Keyword If    '${TEST STATUS}' == 'FAIL'    VideoLogger.Log videos
