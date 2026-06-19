[![GoDoc](https://godoc.org/github.com/msteinert/pam/v2?status.svg)](http://godoc.org/github.com/msteinert/pam/v2)
[![codecov](https://codecov.io/gh/msteinert/pam/graph/badge.svg?token=L1K3UTB065)](https://codecov.io/gh/msteinert/pam)
[![Go Report Card](https://goreportcard.com/badge/github.com/msteinert/pam/v2)](https://goreportcard.com/report/github.com/msteinert/pam/v2)

# Go PAM

This is a Go wrapper for the PAM application API.

## Module support

Go PAM can also used to create PAM modules in a simple way, using the go.

The code can be generated using [pam-moduler](cmd/pam-moduler/moduler.go) and
an example how to use it using `go generate` create them is available as an
[example module](example-module/module.go).

### Modules and PAM applications

The modules generated with go can be used by any PAM application, however there
are some caveats, in fact a Go shared library could misbehave when loaded
improperly. In particular if a Go shared library is loaded and then the program
`fork`s, the library will have an undefined behavior.

This is the case of SSHd that loads a pam library before forking, making any
go PAM library to make it hang.

To solve this case, we can use a little workaround: to ensure that the go
library is loaded only after the program has forked, we can just `dload` it once
a PAM library is called, in this way go code will be loaded only after that the
PAM application has `fork`'ed.

To do this, we can use a very simple wrapper written in C:

```c
#include <dlfcn.h>
#include <limits.h>
#include <security/pam_modules.h>
#include <security/pam_ext.h>

typedef int (*PamHandler)(pam_handle_t *,
                          int          flags,
                          int          argc,
                          const char **argv);

static void
on_go_module_removed (pam_handle_t *pamh,
                      void         *go_module,
                      int           error_status)
{
  dlclose (go_module);
}

static void *
load_module (pam_handle_t *pamh,
             const char   *module_path)
{
  void *go_module;

  if (pam_get_data (pamh, "go-module", (const void **) &go_module) == PAM_SUCCESS)
    return go_module;

  go_module = dlopen (module_path, RTLD_LAZY);
  if (!go_module)
    return NULL;

  pam_set_data (pamh, "go-module", go_module, on_go_module_removed);

  return go_module;
}

static inline int
call_pam_function (pam_handle_t *pamh,
                   const char   *function,
                   int           flags,
                   int           argc,
                   const char  **argv)
{
  char module_path[PATH_MAX] = {0};
  const char *sub_module;
  PamHandler func;
  void *go_module;

  if (argc < 1)
    {
      pam_error (pamh, "%s: no module provided", function);
      return PAM_MODULE_UNKNOWN;
    }

  sub_module = argv[0];
  argc -= 1;
  argv = (argc == 0) ? NULL : &argv[1];

  strncpy (module_path, sub_module, PATH_MAX - 1);

  go_module = load_module (pamh, module_path);
  if (!go_module)
    {
      pam_error (pamh, "Impossible to load module %s", module_path);
      return PAM_OPEN_ERR;
    }

  *(void **) (&func) = dlsym (go_module, function);
  if (!func)
    {
      pam_error (pamh, "Symbol %s not found in %s", function, module_path);
      return PAM_OPEN_ERR;
    }

  return func (pamh, flags, argc, argv);
}

#define DEFINE_PAM_WRAPPER(name) \
  PAM_EXTERN int \
    (pam_sm_ ## name) (pam_handle_t * pamh, int flags, int argc, const char **argv) \
  { \
    return call_pam_function (pamh, "pam_sm_" #name, flags, argc, argv); \
  }

DEFINE_PAM_WRAPPER (acct_mgmt)
DEFINE_PAM_WRAPPER (authenticate)
DEFINE_PAM_WRAPPER (chauthtok)
DEFINE_PAM_WRAPPER (close_session)
DEFINE_PAM_WRAPPER (open_session)
DEFINE_PAM_WRAPPER (setcred)
```

## Testing

To run the full suite, the tests must be run as the root user. To setup your
system for testing, create a user named "test" with the password "secret". For
example:

```
$ sudo useradd test \
    -d /tmp/test \
    -p '$1$Qd8H95T5$RYSZQeoFbEB.gS19zS99A0' \
    -s /bin/false
```

Then execute the tests:

```
$ sudo GOPATH=$GOPATH $(which go) test -v
```

Other tests can instead run as user without any setup with
normal `go test ./...`

[1]: http://godoc.org/github.com/msteinert/pam/v2
[2]: http://www.linux-pam.org/Linux-PAM-html/Linux-PAM_ADG.html
