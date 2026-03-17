Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show

Hide
TypeUsername "user-needs-reset-integration-mandatory@example.com"
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
Wait+CLIPrompt /New password/ /Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "authd2404"
Show

Hide
Enter
Wait+CLIPrompt /Confirm password/ /Press escape key to go back to choose the provider/
TypeCLIPassword "authd2404"
Show

Hide
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
