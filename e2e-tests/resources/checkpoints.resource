*** Settings ***
Documentation       Reusable test-checkpoint keywords for expensive setup steps.
...
...                 A *checkpoint* is a named virsh snapshot that captures the VM
...                 after a sequence of costly setup steps — most notably the
...                 browser-based QR-code device-authentication flow — so that
...                 multiple test cases can share the result instead of repeating
...                 those steps individually.
...
...                 How it works
...                 ------------
...                 1. The *first* test to call a checkpoint setup keyword claims
...                    the checkpoint, restores the base snapshot, performs the
...                    setup steps, creates a new virsh snapshot, then marks the
...                    checkpoint available.
...                 2. Every *subsequent* test restores directly from the saved
...                    snapshot, skipping all setup steps.
...                 3. If two parallel robot invocations race (same broker/release,
...                    different test subsets), the second one detects ``in_progress``
...                    and waits until the first completes before restoring.
...
...                 Checkpoint snapshots persist across test runs (they are not
...                 deleted by the test teardown).  If the snapshot already exists
...                 when a new run starts, every test restores it immediately
...                 without going through the claim logic.  To force recreation,
...                 delete the snapshot manually with ``virsh snapshot-delete``.
...
...                 Available checkpoints
...                 ---------------------
...                 ``authd-user-created``
...                     Captured after the remote user logs in via CLI QR-code
...                     device authentication and then logs out.  The VM is at the
...                     GNOME desktop (ubuntu session, no terminal windows open).
...                     ``%{E2E_USER}`` is registered in authd's database and the
...                     local password is ``${local_password}``.
...
...                     Use when tests need the user registered but do not need an
...                     active GDM session::
...
...                         Test Setup    checkpoints.authd User Created
...
...                 ``authd-user-logged-in-via-gdm``
...                     Captured after the remote user logs in via GDM QR-code
...                     device authentication.  The VM is at the GNOME desktop
...                     with the authd user's session active.  ``%{E2E_USER}``
...                     is registered in authd's database, the local password is
...                     ``${local_password}``, and the GNOME login keyring is unlocked.
...
...                     Use when tests need the user already authd User Logged In Via GDM
...                     with an active GNOME session::
...
...                         Test Setup    checkpoints.authd User Logged In Via GDM

Resource            resources/utils.resource
Resource            resources/broker.resource
Resource            resources/authd.resource
Library             ./Checkpoint.py    AS    Checkpoint


*** Keywords ***
authd User Created
    [Documentation]    Test setup for tests that need a remote user already
    ...    registered via CLI QR-code device authentication.
    ...
    ...    After this keyword returns, the VM is at the GNOME desktop with:
    ...
    ...    - ``%{E2E_USER}`` registered in authd's database.
    ...    - Local password set to ``${local_password}``.
    ...    - No terminal windows open.
    ...    - Journal and VNC recording started.
    ...
    ...    The first test to call this keyword performs the full browser-based
    ...    QR-code login and creates a VM snapshot named
    ...    ``%{BROKER}-checkpoint-authd-user-created``.  All subsequent tests
    ...    restore directly from that snapshot.
    Set Suite Metadata    Ubuntu Version    %{RELEASE}    top=True
    Set Suite Metadata    Broker    %{BROKER}    top=True
    Checkpoint.Setup Checkpoint
    ...    authd-user-created
    ...    _Reach authd User Created Checkpoint


_Reach authd User Created Checkpoint
    [Documentation]    Performs the steps to reach the ``authd-user-created``
    ...    checkpoint state from the ``%{BROKER}-installed`` base snapshot.
    ...
    ...    When this keyword returns the VM is at the GNOME desktop:
    ...    - ``%{E2E_USER}`` is registered in authd's database.
    ...    - Local password is ``${local_password}``.
    ...    - Groups are resolved (msentraid: waits up to 120s).
    ...    - No terminal windows open.
    Log In
    Open Terminal
    Log In With Remote User Through CLI: QR Code    %{E2E_USER}    ${local_password}
    # Wait for authd to commit user data before snapshotting.
    Wait Until Keyword Succeeds    30s    1s    Check User Information    %{E2E_USER}
    IF    '${BROKER_SNAP_NAME}' == 'authd-msentraid'
        # The msentraid broker fetches group membership asynchronously after login;
        # wait for it to complete so the snapshot captures the fully provisioned state.
        Wait Until Keyword Succeeds    120s    1s
        ...    Check User Groups    %{E2E_USER}    ${remote_group}
    END
    Log Out From Terminal Session
    # Verify we are back at the host shell before exiting.
    Match Text    ubuntu@ubuntu    10
    # Exit the host shell so the terminal window closes automatically
    # without a "Close terminal?" confirmation dialog.
    Hid.Type String    exit
    Hid.Keys Combo    Return
    # Wait for the terminal window to disappear and the desktop to be clean.
    Wait Until Desktop Ready


authd User Logged In Via GDM
    [Documentation]    Test setup for tests that need a remote user already
    ...    authd User Logged In Via GDM QR-code device authentication.
    ...
    ...    After this keyword returns, the VM is at the GNOME desktop with the
    ...    authd user's session active:
    ...
    ...    - ``%{E2E_USER}`` registered in authd's database.
    ...    - Local password set to ``${local_password}``.
    ...    - GNOME login keyring unlocked.
    ...    - Journal and VNC recording started.
    ...
    ...    The first test to call this keyword performs the full browser-based
    ...    QR-code GDM login and creates a VM snapshot named
    ...    ``%{BROKER}-checkpoint-authd-user-logged-in-via-gdm``.  All subsequent tests
    ...    restore directly from that snapshot.
    Set Suite Metadata    Ubuntu Version    %{RELEASE}    top=True
    Set Suite Metadata    Broker    %{BROKER}    top=True
    Checkpoint.Setup Checkpoint
    ...    authd-user-logged-in-via-gdm
    ...    _Reach authd User Logged In Via GDM Checkpoint


_Reach authd User Logged In Via GDM Checkpoint
    [Documentation]    Performs the steps to reach the ``authd-user-logged-in-via-gdm``
    ...    checkpoint state from the ``%{BROKER}-installed`` base snapshot.
    ...
    ...    When this keyword returns the VM is at the GNOME desktop with the
    ...    authd user's session active:
    ...    - ``%{E2E_USER}`` is registered in authd's database.
    ...    - Local password is ``${local_password}``.
    ...    - GNOME login keyring is unlocked.
    ...    - Groups are resolved (msentraid: waits up to 120s).
    Log In With Remote User Through GDM: QR Code    %{E2E_USER}    ${local_password}
    # Wait for authd to commit user data before snapshotting.
    Wait Until Keyword Succeeds    30s    1s    Check User Information    %{E2E_USER}
    IF    '${BROKER_SNAP_NAME}' == 'authd-msentraid'
        # The msentraid broker fetches group membership asynchronously after login;
        # wait for it to complete so the snapshot captures the fully provisioned state.
        Wait Until Keyword Succeeds    120s    1s
        ...    Check User Groups    %{E2E_USER}    ${remote_group}
    END
    # Verify the GNOME login keyring is unlocked before snapshotting.
    Check that GNOME keyring is unlocked
