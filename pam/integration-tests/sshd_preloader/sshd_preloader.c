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
#include <errno.h>
#include <pwd.h>
#include <shadow.h>
#include <limits.h>
#include <sys/types.h>

#define AUTHD_TEST_SHELL "/bin/sh"
#define AUTHD_TEST_GECOS ""
#define AUTHD_DEFAULT_SSH_PAM_SERVICE_NAME "sshd"
#define AUTHD_SPECIAL_USER_ACCEPT_ALL "authd-test-user-sshd-accept-all@example.com"

#define SIZE_OF_ARRAY(a) (sizeof ((a)) / sizeof (*(a)))

typedef struct {
  struct passwd parent;

  char *authd_name;
  char *home_path;
} MockPasswd;

static MockPasswd passwd_entities[512];
static atomic_int passwd_entities_count;

__attribute__((constructor))
void constructor (void)
{
  fprintf (stderr, "sshd_preloader[%d]: Library loaded\n", getpid ());
}

__attribute__((destructor))
void destructor (void)
{
  for (size_t i = 0; i < SIZE_OF_ARRAY (passwd_entities); ++i)
    {
      free (passwd_entities[i].authd_name);
      free (passwd_entities[i].home_path);
    }

  fprintf (stderr, "sshd_preloader[%d]: Library unloaded\n", getpid ());
}

static const char *
get_home_base_path (void)
{
  const char *base_path = getenv ("AUTHD_TEST_SSH_HOME_BASE");

  if (base_path == NULL)
    return "/not-existing-home";

  return base_path;
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

static uid_t
get_effective_uid (void)
{
  static uid_t effective_uid = (uid_t) -1;

  if (effective_uid == (uid_t) -1)
    effective_uid = getuid () != 0 ? getuid () : 65534;

  return effective_uid;
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
  struct passwd *source_entity = NULL;
  int entity_idx;
#ifdef AUTHD_TESTS_SSH_USE_AUTHD_NSS
  struct passwd *nss_entity = NULL;
#endif

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
      nss_entity = passwd_entity;
      source_entity = passwd_entity;

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
  else
    {
      /* NSS lookup failed (e.g. user has not yet authenticated with authd, or
       * authd socket is unreachable). Fall through to create a fake passwd
       * entry so that sshd's check_pam_user() can match it against PAM_USER.
       */
      fprintf (stderr, "sshd_preloader[%d]: User %s not found via NSS, "
               "creating fake entry\n", getpid (), name);
    }
#endif /* AUTHD_TESTS_SSH_USE_AUTHD_NSS */

  for (size_t i = atomic_load (&passwd_entities_count); i != 0; --i)
    {
      struct passwd *cached_entity = &passwd_entities[i].parent;

      if (!cached_entity->pw_name || strcmp (cached_entity->pw_name, name) != 0)
        continue;

      passwd_entity = cached_entity;

#ifdef AUTHD_TESTS_SSH_USE_AUTHD_NSS
      /* Update the cached entry with the latest NSS data so that fields
       * like pw_dir reflect the broker-assigned home directory once the
       * user has been authenticated and is known to authd.
       */
      if (nss_entity)
        {
          passwd_entity->pw_dir = nss_entity->pw_dir;
          passwd_entity->pw_gecos = nss_entity->pw_gecos;
          passwd_entity->pw_shell = nss_entity->pw_shell;
        }
#endif

      fprintf (stderr, "sshd_preloader[%d]: Recycling fake entity for user %s\n",
               getpid (), name);
      return passwd_entity;
    }

  entity_idx = atomic_fetch_add_explicit (&passwd_entities_count, 1,
                                          memory_order_relaxed);
  assert (entity_idx < SIZE_OF_ARRAY (passwd_entities));

  if (source_entity && source_entity->pw_name &&
      strcasecmp (source_entity->pw_name, name) != 0)
    source_entity = NULL;

  if (source_entity)
    passwd_entities[entity_idx].parent = *source_entity;

  passwd_entity = &passwd_entities[entity_idx].parent;
  assert (passwd_entity->pw_name == NULL ||
          strcasecmp (passwd_entity->pw_name, name) == 0);

  if (passwd_entity->pw_name == NULL)
    {
      MockPasswd *mock_passwd = &passwd_entities[entity_idx];

      passwd_entity->pw_shell = AUTHD_TEST_SHELL;
      passwd_entity->pw_gecos = AUTHD_TEST_GECOS;
      passwd_entity->pw_name = (char *) name;

      /* Construct a per-user home path from the base dir + username,
       * so each user gets their own home directory even in the shared
       * sshd case where AUTHD_TEST_SSH_USER is the accept-all user.
       */
      free (mock_passwd->home_path);
      if (asprintf (&mock_passwd->home_path, "%s/%s",
                     get_home_base_path (), name) < 0)
        mock_passwd->home_path = NULL;
      passwd_entity->pw_dir = mock_passwd->home_path
                                ? mock_passwd->home_path
                                : (char *) get_home_base_path ();

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
   *
   * When running as root (e.g. inside an LXD container), avoid assigning
   * uid/gid 0 to fake users. OpenSSH applies PermitRootLogin checks and other
   * special handling for uid=0 users, which can cause non-deterministic
   * disconnect behavior when authentication fails. Use the uid of "nobody"
   * (65534) as a safe non-root fallback. Auth-failure tests never actually
   * set up a user session, so the uid only needs to pass pre-auth checks.
   */
  passwd_entity->pw_uid = get_effective_uid ();
  passwd_entity->pw_gid = getgid () != 0 ? getgid () : 65534;

  fprintf (stderr, "sshd_preloader[%d]: Simulating to be fake user %s (%d:%d)\n",
           getpid (), passwd_entity->pw_name, passwd_entity->pw_uid,
           passwd_entity->pw_gid);

  return passwd_entity;
}

/*
 * Override getpwnam_r() so that pam_modutil_getpwnam() (which uses
 * getpwnam_r internally) can also find our fake test users.
 * Without this, pam_mkhomedir fails to look up the user and does not
 * create the home directory, causing sshd to print
 * "Could not chdir to home directory".
 */
int
getpwnam_r (const char *name, struct passwd *pwd, char *buf, size_t buflen,
            struct passwd **result)
{
  static int (*orig_getpwnam_r) (const char *, struct passwd *, char *,
                                 size_t, struct passwd **) = NULL;

  if (orig_getpwnam_r == NULL)
    {
      orig_getpwnam_r = dlsym (RTLD_NEXT, "getpwnam_r");
      assert (orig_getpwnam_r);
    }

  if (!is_valid_test_user (name))
    return orig_getpwnam_r (name, pwd, buf, buflen, result);

#ifdef AUTHD_TESTS_SSH_USE_AUTHD_NSS
  /* Try the real NSS lookup first (e.g. via authd NSS module). */
  int ret = orig_getpwnam_r (name, pwd, buf, buflen, result);
  if (ret != 0)
    return ret;
  if (*result != NULL)
    {
      /* Override uid/gid like getpwnam does.
       * Avoid uid/gid 0 for fake users in root environments (e.g. LXD
       * containers) for the same reason as in getpwnam(): uid=0 triggers
       * OpenSSH's root-login special handling (PermitRootLogin checks, etc.).
       * Use nobody (65534) as a safe non-root fallback when running as root.
       */
      pwd->pw_uid = get_effective_uid ();
      pwd->pw_gid = getgid () != 0 ? getgid () : 65534;
      return 0;
    }
#endif

  /* Fall back to our getpwnam() override which creates fake users. */
  struct passwd *pw = getpwnam (name);
  if (pw == NULL)
    {
      *result = NULL;
      return 0;
    }

  *pwd = *pw;
  *result = pwd;
  return 0;
}

/*
 * Override getpwuid() so that lookups by the fake UID (e.g. login_get_lastlog
 * in OpenSSH 10.2+) resolve to the current test user rather than failing.
 * Without this, sshd aborts the PTY allocation when it cannot find an account
 * for uid=<effective_uid>, causing the session to close immediately after
 * authentication.
 */
struct passwd *
getpwuid (uid_t uid)
{
  static struct passwd pw;
  struct passwd *result = NULL;
  static char buf[4096];

  if (getpwuid_r (uid, &pw, buf, sizeof (buf), &result) != 0)
    return NULL;

  return result;
}

/*
 * Override getpwuid_r() for the same reason as getpwuid().
 */
int
getpwuid_r (uid_t uid, struct passwd *pwd, char *buf, size_t buflen,
            struct passwd **result)
{
  static int (*orig_getpwuid_r) (uid_t, struct passwd *, char *, size_t,
                                 struct passwd **) = NULL;

  if (orig_getpwuid_r == NULL)
    {
      orig_getpwuid_r = dlsym (RTLD_NEXT, "getpwuid_r");
      assert (orig_getpwuid_r);
    }

  if (uid != get_effective_uid ())
    return orig_getpwuid_r (uid, pwd, buf, buflen, result);

  /* Return the first cached fake-user entry whose UID matches. */
  for (size_t i = 0; i < (size_t) atomic_load (&passwd_entities_count); ++i)
    {
      struct passwd *cached = &passwd_entities[i].parent;

      if (cached->pw_name && cached->pw_uid == uid)
        {
          fprintf (stderr, "sshd_preloader[%d]: getpwuid_r(%d) - returning cached entry for %s\n",
                   getpid (), (int) uid, cached->pw_name);
          *pwd = *cached;
          *result = pwd;
          return 0;
        }
    }

  *result = NULL;
  return 0;
}

/*
 * Override getspnam_r() so that shadow lookups for test users don't go through
 * the real NSS chain. On hosts with authd installed, /etc/nsswitch.conf
 * includes authd in the shadow line. pam_unix may then delegate password checks
 * to unix_chkpwd, which executes with an empty environment and can only see the
 * default NSS socket path. By returning a fake shadow entry here, we keep the
 * lookup inside sshd and avoid host-dependent NSS behavior.
 *
 * The fake shadow entry has an invalid password hash ("x"), so pam_unix.so
 * will always fail authentication - which is the expected behavior for tests.
 */
int
getspnam_r (const char *name, struct spwd *spbuf, char *buf, size_t buflen,
            struct spwd **spbufp)
{
  static int (*orig_getspnam_r) (const char *, struct spwd *, char *,
                                 size_t, struct spwd **) = NULL;

  if (orig_getspnam_r == NULL)
    {
      orig_getspnam_r = dlsym (RTLD_NEXT, "getspnam_r");
      assert (orig_getspnam_r);
    }

  /* Only intercept test users; let real users go through normal NSS. */
  if (!is_valid_test_user (name))
    return orig_getspnam_r (name, spbuf, buf, buflen, spbufp);

  fprintf (stderr, "sshd_preloader[%d]: getspnam_r(%s) - returning fake shadow entry\n",
           getpid (), name);

  /* Create a minimal fake shadow entry.
   * sp_pwdp = "x" means password is stored elsewhere (but we don't have it),
   * which will cause pam_unix.so to fail authentication as expected.
   */
  size_t name_len = strlen (name) + 1;
  if (buflen < name_len + 2)
    {
      /* Buffer too small */
      *spbufp = NULL;
      return ERANGE;
    }

  /* Copy name into buffer */
  char *name_buf = buf;
  strcpy (name_buf, name);

  /* Copy password hash "x" into buffer after the name */
  char *pwdp_buf = buf + name_len;
  strcpy (pwdp_buf, "x");

  spbuf->sp_namp = name_buf;
  spbuf->sp_pwdp = pwdp_buf;
  spbuf->sp_lstchg = -1;
  spbuf->sp_min = -1;
  spbuf->sp_max = -1;
  spbuf->sp_warn = -1;
  spbuf->sp_inact = -1;
  spbuf->sp_expire = -1;
  spbuf->sp_flag = 0;

  *spbufp = spbuf;
  return 0;
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
