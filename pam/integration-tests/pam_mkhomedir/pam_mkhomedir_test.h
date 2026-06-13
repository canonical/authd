/* Compatibility layer with private definitions coming from upstream PAM
 * Same license applies here.
 */

#pragma once

#include <stdio.h>
#include <string.h>
#include <stdarg.h>

extern char **environ;

static inline const char *
pam_str_skip_prefix(const char *str, const char *prefix)
{
  size_t prefix_len = prefix ? strlen (prefix) : 0;
	return strncmp(str, prefix, prefix_len) ? NULL : str + prefix_len;
}

static inline char * PAM_FORMAT((printf, 1, 2)) PAM_NONNULL((1)) __attribute__((__malloc__))
pam_asprintf(const char *fmt, ...)
{
	int rc;
	char *res;
	va_list ap;

	va_start(ap, fmt);
	rc = vasprintf(&res, fmt, ap);
	va_end(ap);

	return rc < 0 ? NULL : res;
}

#define _(V) V
#define UNUSED __attribute__((__unused__))

/* argument parsing */
#define MKHOMEDIR_DEBUG      020	/* be verbose about things */
#define MKHOMEDIR_QUIET      040	/* keep quiet about things */

#define LOGIN_DEFS           "/etc/login.defs"
#define UMASK_DEFAULT        "0022"

# define DIAG_PUSH_IGNORE_CAST_QUAL					\
	_Pragma("GCC diagnostic push");					\
	_Pragma("GCC diagnostic ignored \"-Wcast-qual\"")
# define DIAG_POP_IGNORE_CAST_QUAL					\
	_Pragma("GCC diagnostic pop")
