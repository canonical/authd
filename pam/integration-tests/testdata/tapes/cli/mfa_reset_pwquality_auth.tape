Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show

Hide
TypeUsername "user-mfa-with-reset"
Show

Hide
Enter
Wait+Screen /Select your provider/
Wait+Screen /2. ExampleBroker/
Show

Hide
Type "2"
Wait+CLIPrompt /Gimme your password/ /Press escape key to go back to select the authentication method/
Show

Hide
TypeCLIPassword "goodpass"
Show

Hide
Enter
Wait+Screen@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT} * 1.2 /Password reset, 2 step\(s\) missing/
Wait /Press escape key to go back to select the authentication method/
Show

Hide
Wait+Screen /Plug your fido device and press with your thumb/
Wait /Press escape key to go back to select the authentication method/
Show

Hide
Wait+Screen@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT} * 1.2 /Password reset, 1 step\(s\) missing/
Wait /Press escape key to go back to select the authentication method/
Show

# Wait for MFA auth to happen
Hide
Wait+Screen /Enter your new password \(3 days until mandatory\)\n/
Wait+CLIPrompt@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT} * 1.2 /New password/ /\[ Skip \]/ /[ ]*Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "password"
Show

Hide
Enter
Wait+CLIPrompt@${AUTHD_WAIT_DEFAULT}*2 /New password/ /\[ Skip \]\nThe password fails the dictionary check[^\n]+[\n]+[ ]*Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "1234"
Show

Hide
Enter
Wait+CLIPrompt@${AUTHD_WAIT_DEFAULT}*2 /New password/ /\[ Skip \]\nThe password is shorter than \d+ characters[\n]+[ ]*Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "authd2404"
Show

Hide
Enter
Wait+CLIPrompt@${AUTHD_WAIT_DEFAULT}*2 /Confirm password/ /\[ Skip \]/ /[ ]*Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "authd2404"
Show

Hide
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
