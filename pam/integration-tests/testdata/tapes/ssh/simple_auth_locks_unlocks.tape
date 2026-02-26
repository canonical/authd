Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait+Prompt /Choose your provider/
Show

Hide
TypeInPrompt "2"
Show

Hide
Enter
Wait+Prompt /Gimme your password/
Show

Hide
Type "goodpass"
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show

ClearTerminal

Hide
TypeInPrompt+Shell "${AUTHCTL_PATH} user lock ${AUTHD_PAM_SSH_USER}"
Enter
Wait
Show

ClearTerminal

Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait+Prompt /Gimme your password/
Show

Hide
Type "goodpass"
Enter
Wait /permission denied: user .* is locked/
Wait
Show

ClearTerminal

# lock again...

Hide
TypeInPrompt+Shell "${AUTHCTL_PATH} user lock ${AUTHD_PAM_SSH_USER}"
Enter
Wait
Show

ClearTerminal

Hide
TypeInPrompt+Shell "${AUTHCTL_PATH} user unlock ${AUTHD_PAM_SSH_USER}"
Enter
Wait
Show

Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait+Prompt /Gimme your password/
Show

Hide
Type "goodpass"
Enter
${AUTHD_TEST_TAPE_COMMAND_AUTH_FINAL_WAIT}
Show
