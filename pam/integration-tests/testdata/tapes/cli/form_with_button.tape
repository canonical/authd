Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show

Hide
TypeUsername "user-integration-form-w-button@example.com"
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
Wait+Screen /7\. Authentication code/
Show

Hide
Type "7"
Wait+CLIPrompt@${AUTHD_WAIT_DEFAULT}*2 /Enter your one time credential/ /\[ Resend SMS \(1 sent\) \][\n]+[ ]*Press escape key to go back to select the authentication method/
Show

Hide
Tab
Show

Hide
Enter
Wait+CLIPrompt@${AUTHD_WAIT_DEFAULT}*2 /Enter your one time credential/ /\[ Resend SMS \(2 sent\) \][\n]+[ ]*Press escape key to go back to select the authentication method/
Show

Hide
TypeInPrompt "temporary pass00"
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
