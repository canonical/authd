// TiCS: disabled // This is a test helper.

#define _GNU_SOURCE 1
#include <assert.h>
#include <stdlib.h>
#include <stdio.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <unistd.h>
#include <sys/types.h>
#include <dlfcn.h>
#include <string.h>
#include <ctype.h>
#include <pwd.h>
#include <limits.h>
#include <sys/types.h>

#define AUTHD_TEST_SHELL "/bin/sh"
#define AUTHD_TEST_GECOS ""
#define AUTHD_DEFAULT_SSH_PAM_SERVICE_NAME "sshd"
#define AUTHD_SPECIAL_USER_ACCEPT_ALL "authd-test-user-sshd-accept-all"

#define SIZE_OF_ARRAY(a) (sizeof ((a)) / sizeof (*(a)))

typedef struct {
  struct passwd parent;

  char *authd_name;
} MockPasswd;

static MockPasswd passwd_entities[512];

__attribute__((constructor))
void constructor (void)
{
  fprintf (stderr, "sshd_preloader[%d]: Library loaded\n", getpid ());
}

__attribute__((destructor))
void destructor (void)
{
  for (size_t i = 0; i < SIZE_OF_ARRAY (passwd_entities); ++i)
    free (passwd_entities[i].authd_name);

  fprintf (stderr, "sshd_preloader[%d]: Library unloaded\n", getpid ());
}

static const char *
get_home_path (void)
{
  const char *home_path = getenv ("AUTHD_TEST_SSH_HOME");

  if (home_path == NULL)
    return "/not-existing-home";

  return home_path;
}

static bool
is_supported_test_fake_user (const char *name)
{
  /* Further special case for the 'r' user */
  if (strcmp (name, "r") == 0)
    return true;

  return false;
}

static bool
is_valid_test_user (const char *name)
{
  static const char *test_user = NULL;

  if (!test_user)
    test_user = getenv ("AUTHD_TEST_SSH_USER");

  if (test_user == NULL || *test_user == '\0')
    return false;

  if (strcasecmp (test_user, name) == 0)
    return true;

  if (strcasecmp (test_user, AUTHD_SPECIAL_USER_ACCEPT_ALL) != 0)
    return false;

  /* Here we accept all the users supported by the example broker */
  if (strncasecmp (name, "user", 4) == 0 && strlen (name) > 4)
    return true;

  return is_supported_test_fake_user (name);
}

static bool
is_lower_case (const char *str)
{
  for (size_t i = 0; str[i]; ++i)
    {
      if (isalpha (str[i]) && !islower (str[i]))
        return false;
    }

  return true;
}

/*
 * This overrides allows us to manually handle the getpwnam() ensuring that
 * we reply a fake user only when an expected fake user is requested.
 * To handle this we could even have used __nss_configure_lookup()
 * with a fake module or our own, but this preloader is meant to be for
 * testing the behavior of the PAM module only and we want it to be fully
 * predictable for each test.
 */
struct passwd *
getpwnam (const char *name)
{
  static struct passwd * (*orig_getpwnam) (const char *name) = NULL;
  struct passwd *passwd_entity = NULL;
  static atomic_int last_entity_idx;
  int entity_idx;

  if (orig_getpwnam == NULL)
    {
      orig_getpwnam = dlsym (RTLD_NEXT, "getpwnam");
      assert (orig_getpwnam);
    }

  if (!is_valid_test_user (name))
    {
      fprintf (stderr, "sshd_preloader[%d]: User %s is not a test user\n",
               getpid (), name);

      return orig_getpwnam (name);
    }

#ifdef AUTHD_TESTS_SSH_USE_AUTHD_NSS
  if ((passwd_entity = orig_getpwnam (name)))
    {
      fprintf (stderr, "sshd_preloader[%d]: Simulating to be the broker user %s (%d:%d)\n",
               getpid (), passwd_entity->pw_name, passwd_entity->pw_uid,
               passwd_entity->pw_gid);

      if (strcmp (name, "root") == 0)
        {
          assert (passwd_entity->pw_uid == 0);
          assert (passwd_entity->pw_gid == 0);
        }
      else
        {
          assert (passwd_entity->pw_uid != 0);
          assert (passwd_entity->pw_gid != 0);
        }

      /* Ensure the GID we got matches the UID.
       * See: https://wiki.debian.org/UserPrivateGroups#UPGs
       */
      if (passwd_entity->pw_uid != passwd_entity->pw_gid)
        {
          fprintf (stderr, "sshd_preloader[%d]: User %s has different UID and GID (%d:%d)\n",
                   getpid (), name, passwd_entity->pw_uid, passwd_entity->pw_gid);
          abort();
        }
    }
  else if (!is_supported_test_fake_user (name))
    {
      fprintf (stderr, "sshd_preloader[%d]: User %s is not handled by authd brokers\n",
               getpid (), name);
      return NULL;
    }
#endif /* AUTHD_TESTS_SSH_USE_AUTHD_NSS */

  for (size_t i = atomic_load (&last_entity_idx); i != 0; --i)
    {
      passwd_entity = &passwd_entities[i].parent;

      if (!passwd_entity->pw_name || strcmp (passwd_entity->pw_name, name) != 0)
        continue;

      fprintf (stderr, "sshd_preloader[%d]: Recycling fake entity for user %s\n",
               getpid (), name);
      return passwd_entity;
    }

  entity_idx = atomic_fetch_add_explicit (&last_entity_idx, 1,
                                          memory_order_relaxed);
  assert (entity_idx < SIZE_OF_ARRAY (passwd_entities));

  if (passwd_entity)
    passwd_entities[entity_idx].parent = *passwd_entity;

  passwd_entity = &passwd_entities[entity_idx].parent;
  assert (passwd_entity->pw_name == NULL ||
          strcasecmp (passwd_entity->pw_name, name) == 0);

  if (passwd_entity->pw_name == NULL)
    {
      passwd_entity->pw_shell = AUTHD_TEST_SHELL;
      passwd_entity->pw_gecos = AUTHD_TEST_GECOS;
      passwd_entity->pw_name = (char *) name;
      passwd_entity->pw_dir = (char *) get_home_path ();

      if (!is_lower_case (passwd_entity->pw_name))
        {
          /* authd uses lower-case user names */
          MockPasswd *mock_passwd = (MockPasswd *) passwd_entity;
          char *n = strdup (passwd_entity->pw_name);

          for (size_t i = 0; n[i]; ++i)
            n[i] = tolower (n[i]);

          mock_passwd->authd_name = n;
          passwd_entity->pw_name = mock_passwd->authd_name;

          fprintf (stderr, "sshd_preloader[%d]: User %s converted to %s\n",
                  getpid (), name, passwd_entity->pw_name);
        }
    }

  /* authd uses lower-case user names */
  assert (is_lower_case (passwd_entity->pw_name));

  /* We're simulating to be the same user running the test but with another
   * name, so that we won't touch the user settings, but it's still enough to
   * trick sshd.
   */
  passwd_entity->pw_uid = getuid ();
  passwd_entity->pw_gid = getgid ();

  fprintf (stderr, "sshd_preloader[%d]: Simulating to be fake user %s (%d:%d)\n",
           getpid (), passwd_entity->pw_name, passwd_entity->pw_uid,
           passwd_entity->pw_gid);

  return passwd_entity;
}

FILE *
fopen (const char *pathname, const char *mode)
{
  static FILE * (*orig_fopen) (const char *pathname, const char *mode) = NULL;
  const char *service_path;

  if (!orig_fopen)
    orig_fopen = dlsym (RTLD_NEXT, "fopen");

  service_path = getenv ("AUTHD_TEST_SSH_PAM_SERVICE");

  if (service_path == NULL || pathname == NULL)
    return orig_fopen (pathname, mode);

  if (strcmp (pathname, "/etc/pam.d/" AUTHD_DEFAULT_SSH_PAM_SERVICE_NAME) == 0 ||
      strcmp (pathname, "/usr/lib/pam.d/" AUTHD_DEFAULT_SSH_PAM_SERVICE_NAME) == 0)
    {
      fprintf (stderr, "sshd_preloader[%d]: Trying to open '%s', "
               "but redirecting instead to '%s'\n",
               getpid (), pathname, service_path);
      pathname = service_path;
    }

  return orig_fopen (pathname, mode);
}
