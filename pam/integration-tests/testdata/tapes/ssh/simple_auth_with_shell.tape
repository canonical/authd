Hide
TypeInPrompt+Shell "${AUTHD_TEST_TAPE_COMMAND}"
Enter
Wait+Prompt /Choose your provider/
Show

Hide
TypeInPrompt "2"
Enter
Wait+Prompt /Gimme your password/
Show

Hide
Type "goodpass"
Enter
Wait@${AUTHD_WAIT_DEFAULT}*3 /\$\n/
Show

Hide
TypeInPrompt+SH "echo $USER"
Enter
Wait /user-integration[\w-]+@[\w-.]+\n\$\n/
Show

Hide
TypeInPrompt+SH `[ -n "${SSH_CONNECTION}" ] && echo "Inside SSH"`
Wait+Nth(1) /Inside SSH/
Enter
Wait+Nth(2) /Inside SSH/
Wait /\$\n/
Show

Hide
Ctrl+D
Wait
Show

Hide
TypeInPrompt+Shell "echo ${SSH_CONNECTION:-Outside SSH}"
Enter
Wait
Show
