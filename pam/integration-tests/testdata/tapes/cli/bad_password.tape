Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait /Username: user name\n/
Show

Hide
TypeUsername "user-needs-reset@example.com"
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
Wait+Screen /Password reset, 1 step\(s\) missing/
Wait /Press escape key to go back to select the authentication method/
Show

Hide
Wait+CLIPrompt /New password/ /Press escape key to go back to choose the provider/
Show

Hide
Type ""
Show

Hide
Enter
Wait+CLIPrompt /New password/ /No password supplied/ /[ ]*Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "1234"
Show

Hide
Enter
Wait+CLIPrompt /New password/ /The password is shorter than \d+ characters/ /[ ]*Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "12345678"
Show

Hide
Enter
Wait+CLIPrompt /New password/ /The password fails the dictionary check[^\n]*/ /[ ]*Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "authd2404"
Show

Hide
Enter
Wait+CLIPrompt /Confirm password/ /Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "123456789"
Show

Hide
Enter
Wait+CLIPrompt /New password/ /Password entries don't match/ /[ ]*Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "authd2404"
Show

Hide
Enter
Wait+CLIPrompt /Confirm password/ /Press escape key to go back to choose the provider/
Show

Hide
TypeCLIPassword "authd2404"
Show

Hide
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
