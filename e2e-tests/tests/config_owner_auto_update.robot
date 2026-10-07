*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource
Resource        resources/broker.resource
Resource        resources/checkpoints.resource

# Test Tags       robot:exit-on-failure

Test Setup    checkpoints.authd User Created
Test Teardown   utils.Test Teardown


*** Variables ***
${snapshot}    %{BROKER}-installed
${username}    %{E2E_USER}


*** Test Cases ***
Test that owner is auto-updated in broker configuration
    [Documentation]    This test verifies that when a local user logs in, the
    ...    broker configuration is automatically updated to set the owner
    ...    to the logged-in user.

    # Owner auto-registration happens during checkpoint creation; verify it was set.
    Wait Until Keyword Succeeds    30s    1s    Check If Owner Was Registered    ${username}
