Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show

Hide
TypeUsername "user-mfa@example.com"
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
Escape
Wait+Screen /Select your authentication method/
Wait+Screen /1\. Password authentication/
Show

Hide
Type "1"
Wait+CLIPrompt /Gimme your password/ /Press escape key to go back to select the authentication method/
Show

Hide
TypeCLIPassword "goodpass"
Show

Hide
Enter
Wait+Screen@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT}/2 /Plug your fido device and press with your thumb/
Show

Hide
Escape
Wait+Screen /Select your authentication method/
Wait+Screen /1\. Use your fido device foo/
Show

Hide
Enter
Wait+Screen@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT}/2 /Plug your fido device and press with your thumb/
Wait /Press escape key to go back to select the authentication method/
Show

Hide
Wait+Screen@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT}*1.2 /Unlock your phone \+33\.\.\. or accept request on web interface/
Wait /Press escape key to go back to select the authentication method/
Show

Hide
Escape
Wait+Screen /Select your authentication method/
Wait+Screen /1\. Use your phone \+33\.\.\./
Show

Hide
Enter
Wait+Screen@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT}/2 /Unlock your phone \+33\.\.\. or accept request on web interface/
Wait /Press escape key to go back to select the authentication method/
Show

Hide
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
