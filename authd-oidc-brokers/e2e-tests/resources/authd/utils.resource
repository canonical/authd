*** Settings ***
Documentation       authd test resources

Resource            kvm.resource
Library             ./StringUtils.py    AS    StringUtils
Library             ./Command.py    AS    Command
Library             Process


*** Keywords ***
Log In
    Match Text    Not listed    300
    Hid.Keys Combo    Return
    BuiltIn.Sleep    2
    Hid.Type String    ubuntu
    BuiltIn.Sleep    2
    Hid.Keys Combo    Return
    BuiltIn.Sleep    2
    # Give some time for the desktop to settle
    BuiltIn.Sleep    5
    Hid.Move Pointer To Proportional    0    1
    Match Text    Show Apps    300


Log Out
    Run Command    gnome-session-quit --logout --no-prompt


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
    Hid.Type String    exit
    # We are in a machinectl session, so we need to ^] thrice to exit properly
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]
    Hid.Keys Combo    Control_L    ]


# Hacky way of making the tool recognize that the command finished successfully by running it in the format of:
# $ clear && {command} && echo cmd-finished
Run Command In Terminal
    [Arguments]    ${command}
    Open Terminal
    Hid.Type String   clear && ${command} & clear && wait && echo cmd-finished
    Hid.Keys Combo    Return
    Match Text    cmd-finished    600
    Close Focused Window


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
    Run Command In Terminal    apt-get update
    Run Command In Terminal    apt-get upgrade -y
    Close Terminal In Sudo Mode
