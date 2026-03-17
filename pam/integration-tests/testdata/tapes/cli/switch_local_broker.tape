Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show

Hide
TypeUsername "user-integration-switch-broker@example.com"
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
Show

Hide
Escape
Wait+Screen /Select your provider/
Wait+Screen /1. local/
Show

Hide
Type "1"
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
