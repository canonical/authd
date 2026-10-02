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
    [Documentation]    A local user can change a password with ``passwd`` in a
    ...    terminal and still get password-quality checks.
    ...
    ...    authd is part of the PAM stack, so pam_unix and pam_pwquality must still
    ...    reject weak passwords for unmanaged users. A rejected password must leave
    ...    the old one in place.
    ...
    ...    Scenarios covered (in order):
    ...    - a password shorter than 8 characters is rejected
    ...    - a password that fails the dictionary check is rejected
    ...    - a strong password is accepted
    ...    - the user can log in through GDM with the new password

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
