## Overview

The authd PAM module works in a quite complex way due to some Go limitations
when used as shared library, so we basically have two different modes to use
it.

 1. When used in GDM, we just compile the module as a shared library that
    the gdm pam worker will load and open through the `gdm-authd` PAM service.
    <br/>
    In such mode, the module also communicates to GDM via the PAM extended
    binary protocol, using [JSON format](https://gitlab.gnome.org/GNOME/gdm/-/merge_requests/227).
 2. When used in normal PAM transactions by any other PAM application we
    instead compile the module as an executable application that is launched
    by our [`go-exec`](./go-exec/module.c) PAM module implemented in C
    and that does some D-Bus communication over a private bus with the actual
    PAM application.


### Compiling

#### GDM PAM module

The module can be both generated in release or debug mode:

    go generate -C pam -x

or:

    go generate -C pam -x -tags pam_debug

Either way the PAM library to be loaded by the `gdm-authd` service will be in
`./pam/pam_authd.so`

This can be technically be used also by any other PAM application, but due to
some threading race (as per the go multithreading nature), it's known to be
hanging when loaded by some applications (such as `sshd`).

#### Generic PAM Module

As mentioned in order to make our Go PAM implementation of the PAM module to be
reliable, we relied on a simpler C module that only acts as a wrapper between
the actual PAM APIs and a go program that is called and that communicates with
the actual module using a D-Bus private connection (where the security of that
is both grantee by the fact that it's almost impossible to predict its address,
but also by native D-Bus credentials checks and on child PID verification).

In order to build the module and its relative companion PAM program you can do:

    go generate -C pam -x
    go build -C pam -o $PWD/pam/pam_authd

or:

    go generate -C pam -x -tags pam_debug
    go build -C pam -tags pam_debug -o $PWD/pam/pam_authd

This will build two binaries:
 - `./pam/go-exec/pam_authd_exec.so`: That is the actual PAM module
 - `./pam/pam_authd`: Its companion child PAM application

It's possible to use these in a PAM service file using something like:


    auth    sufficient $AUTHD_SOURCE_PATH/pam/go-exec/pam_authd_exec.so $AUTHD_SOURCE_PATH/pam/pam_authd

It's also possible to provide other arguments that may have effect either on the
module or in its wrapper, for example:

    auth    sufficient $AUTHD_SOURCE_PATH/pam/go-exec/pam_authd_exec.so $AUTHD_SOURCE_PATH/pam/pam_authd --exec-debug debug=true force_native_client=true logfile=/dev/stderr

Se [`go-exec-module.c`](./go-exec/module.c) or [`pam.go`](./pam.go) source
code for further arguments.


## Manual Testing the PAM module

While there's no yet an interactive way to test the GDM mode of the module,
without an actual gnome-shell implementation, it's possible to do some manual
testing of the PAM module as it works when called by tools such as `su`, `sudo`,
`login`, `passwd`, `ssh`...

To perform such testing we've a tool, [`pam-runner.go`](./tools/pam-runner/pam-runner.go),
that is in charge of:
 - Compiling the PAM Exec module
 - Compile the PAM Child application
 - Generate a temporary PAM service file with those files defined
 - Start a PAM transaction via libpam
 - Handle the PAM conversations

The basic login mode can be simulated by running:

    go run -tags=withpamrunner ./pam/tools/pam-runner login

You can specify the `socket` to be used with `socket=$authd_socket_path` or
enable debugging via `debug=true`:

    go run -tags=withpamrunner ./pam/tools/pam-runner login \
        socket=/tmp/authd.sock debug=true

Password change simulation (as done by `passwd`) can be done using the `passwd`
argument:

    go run -tags=withpamrunner ./pam/tools/pam-runner passwd

In order to trigger the native PAM UI (as in `ssh`) instead it's needed to
explicitly request it (as otherwise it's only triggered when a TTY is not
available):

    AUTHD_PAM_CLI_SUPPORTS_CONVERSATION=1 go run -tags withpamrunner \
        ./pam/tools/pam-runner login force_native_client=true


### Troubleshooting

In all these cases, when enabled through `debug=true` parameter, it's possible
to see the debug log both passing the `logfile=` parameter pointing to a
writable path (`/dev/stderr` is valid too!) or leaving it as default and relying
on systemd journal that is possible to watch in realtime with:

    journalctl -ef _COMM=exec-child


Note that to test this with an installed version of authd.
