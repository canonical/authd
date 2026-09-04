// TiCS: disabled // This is a test helper.

/* pam_user_lookup performs the account-stage lookup that broke logins which
 * rename the user: pam_unix runs getpwnam(PAM_USER) during pam_acct_mgmt,
 * after the authentication already renamed the user. Failing with
 * PAM_USER_UNKNOWN when the name no longer resolves makes the SSH integration
 * tests catch a missing or mistimed temporary user alias. pam_unix cannot
 * stand in for it here: it requires shadow data the harness does not provide.
 */

#define PAM_SM_ACCOUNT

#include <pwd.h>
#include <security/pam_ext.h>
#include <security/pam_modules.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

PAM_EXTERN int
pam_sm_acct_mgmt (pam_handle_t *pamh, int flags, int argc,
                  const char **argv)
{
  const char *user = NULL;
  struct passwd passwd_entry;
  struct passwd *result = NULL;
  char buffer[16384];
  bool socket_set = false;
  int pam_status = PAM_SUCCESS;

  (void) flags;

  for (int i = 0; i < argc; ++i)
    {
      static const char socket_prefix[] = "socket=";
      if (strncmp (argv[i], socket_prefix, sizeof (socket_prefix) - 1) == 0)
        {
          if (setenv ("AUTHD_NSS_SOCKET",
                      argv[i] + sizeof (socket_prefix) - 1, 1) != 0)
            return PAM_SYSTEM_ERR;
          socket_set = true;
        }
    }

  pam_status = pam_get_user (pamh, &user, NULL);
  if (pam_status != PAM_SUCCESS)
    goto cleanup;

  int status =
      getpwnam_r (user, &passwd_entry, buffer, sizeof (buffer), &result);
  if (status != 0)
    pam_status = PAM_SYSTEM_ERR;
  else if (result == NULL)
    pam_status = PAM_USER_UNKNOWN;

cleanup:
  if (socket_set && unsetenv ("AUTHD_NSS_SOCKET") != 0)
    return PAM_SYSTEM_ERR;
  return pam_status;
}
