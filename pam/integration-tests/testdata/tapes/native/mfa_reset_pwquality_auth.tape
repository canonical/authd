Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait+Prompt /Choose your provider/
Show

Hide
TypeInPrompt "2"
Show

Hide
Show

Hide
Enter
Wait+Prompt /Gimme your password/
Show

Hide
Type "goodpass"
Enter
Wait+Nth(1)@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT}/2 /Password reset, 2 step\(s\) missing/
Wait+Prompt /Plug your fido device and press with your thumb/
Show

# Wait for MFA auth to happen
Hide
Enter
Wait+Nth(1) /Password reset, 1 step\(s\) missing/
Wait+Prompt /Choose action/
Show

Hide
TypeInPrompt "1"
Show

Hide
Enter
Wait+Prompt@${AUTHD_SLEEP_EXAMPLE_BROKER_MFA_WAIT}*1.2 /Enter your new password \(3 days until mandatory\)/
Show

Hide
Type "password"
Enter
Wait+Nth(1) /The password fails the dictionary check/
Wait+Prompt /Enter your new password \(3 days until mandatory\)/
Show

Hide
Type "1234"
Enter
Wait+Nth(1) /The password is shorter than \d+ characters/
Wait+Prompt /Enter your new password \(3 days until mandatory\)/
Show

Hide
Type "authd2404"
Enter
Wait+Prompt /Confirm Password/
Show

Hide
Type "authd2404"
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
