Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show

Hide
TypeUsername "user-integration-not-empty@example.com"
Show

Hide
Backspace 38
Wait /Username: user name\n/
Show

Hide
Enter
Wait /Username: user name\n/
Show

Hide
Escape
Backspace
TypeUsername "user-integration-was-empty@example.com"
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
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
