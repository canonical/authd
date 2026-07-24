*** Settings ***
Resource        resources/utils.resource
Resource        resources/authd.resource

Resource        resources/broker.resource

# Test Tags       robot:exit-on-failure

Test Setup    utils.Test Setup    snapshot=%{BROKER}-installed
Test Teardown   utils.Test Teardown


*** Variables ***
${new_good_password}    2FuA2M3jfGl
${new_password_too_short}    1234
${new_password_too_simple}    12345678


*** Test Cases ***
Test changing local password of local user with passwd
    [Documentation]    This test verifies that a local user can still change their local password with authd installed.
    ...                It also verifies that the new password is not too short or too simple, and that the user can log in with the new password.

    # Log in with local user
    Log In
    Open Terminal
    
    # Try to set a new password that is too short
    Terminal Password Change Error    ${new_password_too_short}      The password is shorter than 8 characters
    
    # Try to set a new password that is too simple
    Terminal Password Change Error    ${new_password_too_simple}     The password fails the dictionary check
    
    # Set a new password for the local user
    Terminal Password Change     ${new_good_password}
    Close Focused Window
    Log Out

    # Log in with new password
    Log In With Password    ${new_good_password}
