# Manual probe, not part of the CI suite.
#
# Requires authd built with `-tags withexamplebroker` and the branch PAM
# module. The example broker produces a marked denial without an identity
# provider account. The screen must be 1280x800.
#
# From e2e-tests, with the YARF venv active:
#   VNC_PORT=<display> yarf --platform Vnc --outdir /tmp/yarf-manual manual

*** Settings ***
Resource    kvm.resource

*** Variables ***
${user}    user-denied-displayable-integration-watch

*** Test Cases ***
Marked denial in GDM
    Sleep    5
    Hid.Keys Combo    Shift
    Match Text    Not listed?    60
    Hid.Move Pointer To Absolute    510    459
    Click LEFT Button
    Match Text    Username    30    confidence=88
    Hid.Type String    ${user}
    Hid.Keys Combo    Return
    Match Text    Select the broker    120
    Hid.Move Pointer To Absolute    640    531
    Click LEFT Button
    Match Text    Gimme your password    30    confidence=88
    Hid.Type String    goodpass
    Hid.Keys Combo    Return
    Sleep    1
    ${t1}=    Read Text
    Log To Console    AFTER_1S:\n${t1}
    Sleep    3
    ${t2}=    Read Text
    Log To Console    AFTER_4S:\n${t2}
    Sleep    8
    ${t3}=    Read Text
    Log To Console    AFTER_12S:\n${t3}
