Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show

Hide
TypeUsername "user-can-reset@example.com"
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
Wait /Press escape key to go back to select the authentication method/
Show

Hide
Enter
Wait+Screen /Password reset, 1 step\(s\) missing/
Wait /Press escape key to go back to select the authentication method/
Show

Hide
Wait+CLIPrompt@${AUTHD_WAIT_DEFAULT}*2 /New password/ /\[ Skip \][\n]+[ ]*Press escape key to go back to choose the provider/
Show

Hide
Tab
Show

Hide
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
