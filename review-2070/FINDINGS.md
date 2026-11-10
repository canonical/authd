# Findings for PR 2070

Review of `provider-username-field` at `bd0f9215a` against `main`. File and line references are to `bd0f9215a`, because the commits below move some lines.

This branch holds 17 commits on top of `bd0f9215a`, grouped by area. The table below maps each commit to the findings it covers. Take what you want. A fix commit includes its test, and I checked each test by reverting the fix. The CI, e2e and docs commits have no tests. The last commit only adds this file and is not meant to be merged.

Evidence comes from throwaway tests with mock brokers and providers, the CI logs of `bd0f9215a`, code reading and one e2e VM (resolute, with the CI build of `bd0f9215a`). The Robot tests did not run end to end, because the Entra browser login failed. So C3, T1 and T2 are not proven in a full run. On this branch the `internal/users`, `internal/services`, `internal/brokers`, `cmd/authctl` and `brokers` tests pass (the `brokers` tests with default tags and with `withgoogle`), and the linters report 0 issues. Not run: `withmsentraid`, the LXD tests, the NSS integration test and the docs spell check.

## All findings

Priority: P1 blocks the merge, P2 should be fixed before release, P3 is low likelihood or low impact. Origin says whether the problem comes from this PR or from code that was already on `main`.

| ID | Finding | Pri | Origin | Status |
|---|---|---|---|---|
| C1 | gosec G204 fails lint | P1 | Introduced | Fixed |
| C2 | LXD-only test cannot build as root | P1 | Introduced | Fixed, needs a CI run in LXD |
| C3 | e2e helper cannot see the temporary pre-auth user | P1 | Introduced | Fixed, only the helper part proven in a VM |
| C4 | Robot format check fails | P1 | Introduced | Fixed |
| S1 | A v3 session lets a bound account take the owner's old name | P1 | Introduced | Fixed |
| S2 | Owner is denied, or loses `sudo`, on the first login under a new name | P2 | Old limit, new docs claim | Docs only. The real fix is a decision |
| S3 | `Match User` does not apply to a typed alias | P2 | Introduced | Fixed (docs) |
| S4 | Owner state is separate per D-Bus interface | P3 | Old structure, newly reachable | Fixed |
| S5 | Every login waits for the user-management lock | P2 | Introduced | Fixed |
| S6 | An alias owner cannot update its info after a downgrade collision | P2 | Introduced | Fixed |
| S7 | A rename onto a local account name locks the user out | P2 | Introduced | Fixed, needs a decision |
| S8 | Logging in with the Unix name does not teach the alias in the native Entra flow | P3 | Introduced | Docs only |
| S9 | `GetBroker` sends a name owned by a local account to the remote broker | P3 | Introduced | Fixed |
| S10 | `authctl user delete` confirms the typed alias | P2 | Introduced | Fixed |
| S11 | `DeleteUser` removes a cache directory without checking its owner | P3 | Old check, new collision path | Fixed |
| S12 | `EqualFold` and `ToLower` disagree for sigma | P3 | Introduced | Fixed |
| S13 | A missing `os-release` fails an SSH alias login | P3 | Introduced | Open, optional |
| T1 | e2e owner assertion cannot match the quoted value | P2 | Introduced | Fixed |
| T2 | `restart-stale` check cannot fail | P2 | Introduced | Fixed, not run |
| T3 | Behaviours that can break with tests still green | P2 | Introduced | Fixed |
| T4 | Three test harness bugs | P2 | Introduced | Fixed |
| T5 | The alias unlock test never starts locked | P3 | Introduced | Fixed |
| D1 | No broker release is named for renames | P2 | Introduced | Open, needs the version number |
| D2 | Other doc statements are wrong or missing | P2 | Introduced | Fixed |
| L1 | Production methods that only tests call, and other dead code | P3 | Introduced, plus old dead code | Partly fixed |
| L2 | `sshLoginNameKey` is redundant | P3 | Introduced | Open |
| L3 | A broker that omits `name` fails late | P3 | Introduced | Fixed |

## Commits

| # | Commit | Findings |
|---|---|---|
| 1 | `pam/integration-tests: Silence gosec G204 and tidy the alias tests` | C1, the `ssh_test.go` points under "Other observations" |
| 2 | `testutils: Build LXD-only tests as root without VCS stamping` | C2 |
| 3 | `tests: Fix four harness bugs in the alias tests` | T4, T5 |
| 4 | `e2e: Match the quoted owner in the auto-registration check` | T1 |
| 5 | `e2e: Fix the SSH pre-auth test and format the SSH login test` | C3, C4, T2, the e2e points under "Other observations" |
| 6 | `users: Skip the user-management lock for unchanged authenticated logins` | S5, and two T3 tests |
| 7 | `users: Let an alias owner update its info after a downgrade collision` | S6 |
| 8 | `users: Keep the stored alias when an IdP rename hits a local name` | S7 |
| 9 | `pam: Fix the broker choice for local names and the 24.04 SSH check` | S9, S12 |
| 10 | `authctl: Show the Unix name when deleting a user by alias` | S10 |
| 11 | `brokers: Do not accept provider renames on sessions without a Unix name` | S1 |
| 12 | `brokers: Keep the first owner and the cache of another user` | S4, S11 |
| 13 | `users, brokers: Remove production methods only tests call` | L1, in part |
| 14 | `users: Default the new Unix name to the provider username` | L3 |
| 15 | `tests: Cover rename edge cases in the user manager and PAM services` | T3 |
| 16 | `tests: Cover the identity checks, owner and D-Bus API of the broker` | T3 |
| 17 | `docs: Document the owner, sshd and rename limits of provider aliases` | S2, S3, S8, D2 |

## Decisions needed

- S2: on the first login under a provider name that authd does not know yet, authd sends no provider ID and no Unix name, so `OWNER` does not match. With `allowed_users = OWNER` the login is denied. With `ALL` and `owner_extra_groups=sudo` it is granted without `sudo` (broker-level test). On `main` the owner was also compared with the authenticated name, so a renamed owner was denied there too. This PR helps for a login with the old Unix name and not for the first login under the new name, while the new docs claim the opposite. The proper fix ties the owner to the verified provider ID, as proposed in the earlier thread. `config.go:252` runs `NormalizeUsername` on the owner value, which would lowercase a case-sensitive ID. The docs commit only documents the limit.
- S7: a bound account whose IdP name becomes a name that local NSS owns fails every login with `provider username "root" is already used by a system user`. The rejection is deliberate. The fix keeps the stored alias instead and logs a warning. Keep the rejection or take the fix.
- D1: neither the changelog nor `user-management.md` names a broker release that accepts a rename. `broker-0.5.4` still calls `VerifyUsername`, so with authd 0.8 and that broker the new name works and the old Unix name fails (read from the broker-0.5.4 source). Name the first release that contains `verifyAuthenticatedIdentity` in `debian/changelog` and `docs/explanation/user-management.md:73-76`, with one sentence for the upgrade order. The changelog says "restart authd first" and the docs say "then restart authd". There is no commit because the version number is missing.

## Details

CI failures of `bd0f9215a`:

- C1: gosec G204 on `useradd` and `userdel` in `pam/integration-tests/ssh_test.go`.
- C2: `RunTestInLXDOnly` runs `go test` as root, and `go build` then fails with `error obtaining VCS status: exit status 128`. That git refuses a tree owned by another user is inferred. The fix adds `GOFLAGS=-buildvcs=false`. LXD does not run locally.
- C3: `ssh_pre_auth.py:189` raises `KeyError` on resolute and stonking. The NSS module returns pre-auth users only when the caller is sshd or a direct child of sshd (`nss/src/passwd/mod.rs:179-195`), and the helper runs under the login shell of `SSH.Execute`. The fix reads the temporary UID from the authd debug log line `Using new UID <n> for temporary user "<name>"`. In a VM with the CI build, `pwd.getpwnam` in the script raised the same `KeyError` for a held pre-auth connection, and the new `temporary_uid()` returned the UID from the log. The Robot test itself did not run.
- C4: `robocop format` would reformat `e2e-tests/tests/ssh_login.robot`.

Two other CI failures are not caused by this PR. The resolute `build-deb` job failed with a stream error from `proxy.golang.org` while it downloaded `sahilm/fuzzy`. The stonking autopkgtest failure in `TestNativeChangeAuthTok` shows the typed `goodpass` echoed once more in the terminal output, which looks like a timing race. I did not rerun that job or compare it with a run on `main`. The stale temporary UID on sshd builds without Ubuntu's UID check is also not in the findings. The author says it affects `main` too (authd-private#112).

Other findings:

- S1: `brokers/internal/dbusservice/methods.go:44-45` passes `unixUsername=""` for API v3. For a bound session `verifyAuthenticatedIdentity` accepts a changed provider name, and `finishAuth` and `authenticatedUserIsAllowed` then compare the owner with the authenticated name (`broker.go:2775`, `2875`). If the IdP gives Bob the owner's old name, Bob gets the owner's rights. A v3 caller is authd 0.7.x with the new broker, or a 0.8 authd that cached the v3 interface before the broker was updated (not tested). The IdP also has to reuse the name. The same gap lets authd 0.7.1 rename the Unix account through the new broker (read in the v0.7.1 source, not run). Test with `NewSession(bob, ..., "bob-id")` and a mock IdP that returns `owner@email.com` with `owner_extra_groups=sudo`:

  | Case | `allowed_users=OWNER` | `allowed_users=ALL` |
  |---|---|---|
  | v3 `NewSession` | granted, with `sudo` | granted, with `sudo` |
  | v4 `NewSessionForUser(..., bob)` | denied | granted, no `sudo` |

  The fix accepts a changed name on a bound ID only when `session.unixUsername != ""`. Four existing tests that used v3-style `NewSession` moved to `NewSessionForUser`. v3 callers lose rename support.
- S3: sshd applies `Match User` to the typed name, and `AllowUsers`, `DenyUsers` and the group rules to the Unix name (checked in OpenSSH `auth.c` and with `sshd -T` on 10.2p1). The key-login block in `docs/explanation/security.md` therefore does not cover an alias outside its pattern.
- S4: `cloneUserConfig` already gave each D-Bus interface its own owner state on `main`. The v3 to v4 switch in this PR makes it reachable again, so a second user can overwrite the first owner. Low likelihood.
- S5: `UpdateAuthenticatedUser` always takes `userManagementMu` (`internal/users/manager.go:274-289`). On main an unchanged user returned before the lock. With the system passwd lock held by a waiting `RegisterUserPreAuthEntry`, an unchanged `UpdateUser` took 0.7 ms and an unchanged `UpdateAuthenticatedUser` took 3.0 s. The wait can reach about 16 s with a real passwd lock. The fix runs the lock-free pass for authenticated updates too and keeps the re-check and the locked check under the lock. Please confirm that this matches the intent of `36ccdb0d7`.
- S6: account `first` has alias `second`, and a 0.7.x downgrade later created the account `second`. An unchanged login for `first` works. A change of gecos, shell or groups fails with `login name "second" is already in use by user "second"`, because the loop over `loginNames` re-checks an alias the account already owns.
- S8: with the native Entra password flow the stored name is sent to Entra, so logging in with the Unix name does not teach the alias.
- S9: the old `GetBroker` sends a name owned by a local account to the alias owner's remote broker, which locks the local user out of the PAM stack.
- S10: `authctl user delete <alias>` throws the lookup result away. The prompt, the call and the final message use the typed name, while the server deletes the account with that Unix name.
- S11: `DeleteUser` already removed the provider-ID directory without checking its content on `main`. The hashed keys in this PR make a collision possible. Not realistic to exploit.
- S12: `strings.EqualFold` and `ToLower` disagree for `σ` and `ς`, so the 24.04 guard is skipped for such names.
- S13: a missing `/etc/os-release` fails an SSH alias login. Optional fix: fall back to `/usr/lib/os-release` and treat a read error as "not 24.04". Ubuntu derivatives with another `ID` and `VERSION_ID="24.04"` keep the unsafe `PAM_USER` rewrite.
- T1: the generated drop-in now holds `owner = """owner_name"""`, and `e2e-tests/resources/broker.resource:434` asserts `owner = ${username}`. The PR's CI run does not include `config_owner_auto_update.robot`, so it was not caught.
- T2: after a restart of authd the broker sessions are gone, so the `restart-stale` login always failed and its assertion could not catch a bug. The fix drops that connection and keeps the live session that must survive the restart and the new connection after it. `forget_alias` now stores a fixed old alias (`old-alias@example.com`), so the replace-an-alias path runs. An empty alias is the downgrade state, not a rename. The explicit `systemctl restart` is gone because `forget_alias` already stops and starts authd. Not run.
- T3: twelve cases could break with the tests still green. All now have a test: a locked account renamed at the IdP and logging in under an unknown new name, a stored alias equal to a system user name, mixed-case aliases, alias login on an account without a provider ID, `ValidateUsername` removed at `broker.go:233`, `:3152` or `:3296`, the case-insensitive compare at `broker.go:183`, an unbound refresh with a changed name, a rename bound only through the cache symlink, `DeleteUser` arguments per API version, the lock check under the lock, an alias-only change, and a failed update that must not rewrite local groups (`Keep_old_username_in_local_groups_when_rename_fails` passed even with `UpdateUserEntry` and `UpdateGroups` swapped, so it was replaced). The new tests also cover the rename-back login, the `could not check name ... in NSS` branch of `rejectForeignNSSAccount`, the `DeleteUser` fallback when `provider_username` is empty, `GetUserByName` with a known alias, a failed owner registration, the owner name written to disk, and the exported D-Bus interfaces.
- T4: a parent `t` used in parallel subtests, an assertion in a stub on the gRPC goroutine, and a test that needs a session bus.
- T5: the alias unlock test never locked the user first, so it did not exercise unlock.
- D2: the docs now say that `owner` is the Unix name for an existing account, that 26.04 needs `openssh-server` 1:10.2p1-2ubuntu3.7 or later, that the first SSH login under an unlearned name can fail once, that `AllowUsers` reads the last `@` as a host pattern (from `sshd_config(5)`, not run), and that a recycled provider name stays denied while the old account holds it (traced in code, not run). `login-ssh.md` no longer says the SSH name is the provider handle. The changelog has one bullet per change and covers the `PAM_USER` rewrite, the 24.04 SSH limit, schema 4 and the downgrade. Not done: an example for short usernames, which can be written now because a rename already makes the two names differ.
- L1: removed `IsUserLocked`, `RegisterUserPreAuth` and the `registerOwner` wrapper, which only tests called. `brokers.Manager.NewSession` was not test-only, because the V2 D-Bus method called it, so that call now uses `NewSessionForUser(..., "")`. Still open: `UpdateUser` and `UserByName` (many test call sites), `internal/brokers/localbroker.go` (already dead on `main`), the unreachable rename branch in `internal/users/db/update.go:64-86`, the old-schema branch in `internal/users/db/testutils.go`, the reserved-prefix cache key migration in `broker.go`, about 85 lines of duplicate tests in `manager_test.go`, an unused `unixUsername` parameter in `examplebroker`, a duplicate locked check in `IsAuthenticated`, and the `preAuthUsername` cleanup loop (it could delete another connection's record once short Unix names exist). A reviewer estimated about 300 lines, mostly tests and test-only API. This was not measured.
- L2: remove `sshLoginNameKey` and the retry half of `TestSetAuthenticatedPamUser`, or keep them and say which case needs them.
- L3: a broker that sends `provider_username` with an empty `name` failed with `empty username` after the lookup. `resolveIdentity` now uses the provider name as the Unix name.

## Other observations

Smaller points that reviewers noted. None blocks anything, and all of them are in code this PR adds or changes unless stated.

e2e and integration tests:

- Fixed in commit 1: `NotContains "Inside SSH"` in `ssh_test.go` was always true. It now checks that the forced command output is absent. `userdel` runs before `useradd`, so a killed run no longer blocks the next one. An unused `providerAlias` field and a redundant `var err error` are gone.
- Fixed in commit 5: `ssh_login.robot` and `ssh_pre_auth.py` edited the sqlite database with different SQL, and both now use one helper. `Change Broker Configuration allowed_users ALL` now reuses `Change allowed_users In Broker Configuration`.
- `ssh_login.robot:186`: `setsid` without `-w` may return 0 early if bash ever `exec`s `env` directly (low confidence).
- `ssh_test.go:274-280` and `:427-433`: the 26.04 LXD-only tests need a fresh container on every CI run, because the cache only holds 24.04. The cost was not measured.

authd service and PAM tests:

- `internal/services/pam/pam_test.go:1294-1299` and `:145`: the client dialer changed to `passthrough:///authd` and a comment was reworded with no stated reason. Reverting the dialer left the package passing.
- `TestAuthenticationSessionsUseUnixUsername` (`pam_test.go:525-549`) repeats `TestSelectBrokerPassesExistingUnixName` and never calls `IsAuthenticated`.
- `auth_fail_tracker_test.go`: the fake-time test needed `delayAfterFailedAuthentication` to be extracted from `IsAuthenticated`, and `TestAuthFailTracker_ResetWindow_NonZero_ResetsAfterInactivity` (line 44) still sleeps 100 ms. That sleep is older than this PR.
- Missing service-level cases: a new account from a v2 broker without a provider ID, and an SSH pre-auth record under a name that differs from the Unix name. All alias tests except one call the real `user.Lookup`, so a host NSS error other than "unknown user" would fail them.
- The locked-user error order changed. `Error_when_user_is_locked` now only proves that a locked account bound to the current broker is denied. A locked user on another broker sees "already bound", and no test pins that.
- No unit test fails if `canonicalUsername: res.GetPamUser()` (`pam/internal/adapter/authentication.go:77`) or the `setAuthenticatedPamUser` call (`pam/pam.go:401`) is removed. A `""` row and a missing os-release row in `TestSetAuthenticatedPamUser`, and one native integration case with `examplebroker.UserIntegrationProviderAliasPrefix`, would cover them.

authctl and the user service:

- `LockUser` and `UnlockUser` resolve the alias in the service (`resolveName`) and again in the manager.
- The `client` to `userClient` rename in `lock.go` and `unlock.go` is half applied. `set-uid.go:46` still shadows `client`.
- The authctl lock, unlock and set-shell alias tests only check the exit code.

Brokers and D-Bus:

- `newSession` and `deleteUser` in `dbusservice/methods.go` repeat the same `if s.service != nil` block.
- `broker.go:43-46` says v2 brokers ignore `providerID` in `DeleteUser`, but `dbusBroker.DeleteUser` now returns an error for it. `manager.go:175-177` and `BrokerV4.xml` say `unixUsername` is empty when no account was selected, but `pam.go:362-366` also clears it for a selected account without a stored broker.
- The reserved-prefix migration of legacy cache keys (`broker.go:862-868`, `905-919`, `986-997`) is about 25 production lines and 45 test lines for provider IDs that no IdP produces.
- `examplebroker`: `NewSession` takes a `unixUsername` argument that it never reads. It exports only `Broker2` but returns `provider_id`, so deleting its users always logs a warning.

Database:

- In `internal/users/db`, `provider_username IS NULL OR` in the migration is dead (the column is `NOT NULL`), `insertOrUpdateUserByID` repeats the alias defaulting from the manager, and the second round of `TestMigrationAddProviderUsernameColumnIsRetrySafe` repeats the first.
- `users.go` says the alias "must be unique across users", but it is only unique among non-empty values.
- Orphan goldens: `internal/services/pam/testdata/golden/TestSetBroker/*/cache.db` (no such test exists) and `.../TestSetUserID/No_op_if_uid_is_already_set` (still schema 2). These are older than this PR.

Docs:

- An admin cannot check whether a user has a provider ID with the shipped tools, because `authctl` has no command for it. A one-line `sqlite3` query on `/var/lib/authd/authd.sqlite3` would do.
- The docs commit on this branch was not run through the docs spell check. The PR's own spell check passed.
