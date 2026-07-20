"""Robot Framework library to manage Temporary Access Passes (TAP) for e2e tests.

Mints a one-time TAP for a user via the Microsoft Graph API so a passwordless
Entra ID login can be tested without a dedicated account: the broker's
empty-password probe gets a code-entry MFA challenge, and the TAP code is
entered at that prompt.

Requires ``UserAuthenticationMethod.ReadWrite.All`` (or the least-privilege
``UserAuthMethod-TAP.ReadWrite.All``) admin-consented as an Application
permission on the app registration, and the TAP method enabled for the test
user in the tenant's Authentication methods policy.
"""

import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request

from robot.api.deco import keyword, library


@library
class EntraTAP:
    """Manages Entra ID Temporary Access Passes for e2e tests."""

    def _tenant_id_from_issuer(self, issuer_url: str) -> str:
        """Extract the tenant UUID from an Entra issuer URL.

        Targets the Entra v2.0 issuer form
        ``https://login.microsoftonline.com/<tid>/v2.0`` (the value the broker
        is configured with for these tests) and bare tenant GUIDs. Other issuer
        hosts (e.g. ``sts.windows.net``) are not handled, which is deliberate:
        the token endpoint below is a ``login.microsoftonline.com`` URL anyway.
        """
        stripped = issuer_url.rstrip("/")
        # Remove the scheme and split by "/"
        path = stripped.split("://", 1)[-1]
        segments = [s for s in path.split("/") if s]
        # The tenant ID immediately follows the host segment.
        for i, seg in enumerate(segments):
            if "microsoftonline" in seg:
                if i + 1 < len(segments):
                    return segments[i + 1]
        # Fallback: return the whole string if it looks like a bare GUID.
        if len(stripped) == 36 and stripped.count("-") == 4:
            return stripped
        raise ValueError(
            f"Could not extract tenant ID from issuer URL: {issuer_url!r}"
        )

    def _acquire_token(self, tenant_id: str, client_id: str, client_secret: str) -> str:
        """Acquire an app-only token for Microsoft Graph."""
        token_url = (
            f"https://login.microsoftonline.com/{tenant_id}/oauth2/v2.0/token"
        )
        payload = urllib.parse.urlencode(
            {
                "grant_type": "client_credentials",
                "client_id": client_id,
                "client_secret": client_secret,
                "scope": "https://graph.microsoft.com/.default",
            }
        ).encode()
        req = urllib.request.Request(token_url, data=payload, method="POST")
        req.add_header("Content-Type", "application/x-www-form-urlencoded")
        try:
            with urllib.request.urlopen(req) as resp:
                result = json.load(resp)
        except urllib.error.HTTPError as exc:
            body = exc.read().decode(errors="replace")
            raise RuntimeError(
                f"Token request failed ({exc.code}): {body}"
            ) from exc

        if "error" in result:
            raise RuntimeError(
                f"Token error {result['error']!r}: {result.get('error_description')}"
            )
        return result["access_token"]

    def _graph(self, token: str, method: str, path: str, body=None):
        """Perform a Microsoft Graph v1.0 request.

        Returns the parsed JSON body on success, or ``None`` for 204 No Content.
        Raises ``RuntimeError`` on HTTP errors.
        """
        url = f"https://graph.microsoft.com/v1.0{path}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Authorization", f"Bearer {token}")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req) as resp:
                if resp.status == 204:
                    return None
                return json.load(resp)
        except urllib.error.HTTPError as exc:
            body = exc.read().decode(errors="replace")
            raise RuntimeError(
                f"Graph {method} {path} failed ({exc.code}): {body}"
            ) from exc

    @keyword
    def create_tap_for_user(
        self,
        user_upn: str,
        lifetime_in_minutes: int = 60,
        is_usable_once: bool = True,
    ) -> str:
        """Create a Temporary Access Pass for *user_upn* and return the passcode.

        Deletes any existing TAP first, since Entra allows only one per user.
        ``lifetime_in_minutes`` defaults to 60 to satisfy tenants that enforce
        that as their policy minimum. Requires ``AUTHD_MSENTRAID_ISSUER_ID``,
        ``AUTHD_MSENTRAID_CLIENT_ID``, and ``AUTHD_MSENTRAID_CLIENT_SECRET``
        to be set.
        """
        issuer = os.environ["AUTHD_MSENTRAID_ISSUER_ID"]
        client_id = os.environ["AUTHD_MSENTRAID_CLIENT_ID"]
        client_secret = os.environ["AUTHD_MSENTRAID_CLIENT_SECRET"]
        tenant_id = self._tenant_id_from_issuer(issuer)

        token = self._acquire_token(tenant_id, client_id, client_secret)

        tap_path = f"/users/{user_upn}/authentication/temporaryAccessPassMethods"

        # Remove any existing TAP — Graph rejects creation when one is already
        # present (a user can have at most one TAP).
        existing = self._graph(token, "GET", tap_path)
        for method in (existing or {}).get("value", []):
            tap_id = method.get("id")
            if tap_id:
                self._graph(
                    token,
                    "DELETE",
                    f"{tap_path}/{tap_id}",
                )

        result = self._graph(
            token,
            "POST",
            tap_path,
            {
                "lifetimeInMinutes": int(lifetime_in_minutes),
                "isUsableOnce": bool(is_usable_once),
            },
        )
        tap = (result or {}).get("temporaryAccessPass")
        if not tap:
            raise RuntimeError(
                f"TAP creation returned no passcode. Full response: {result!r}"
            )

        # A freshly minted TAP isn't always usable immediately; poll until
        # Graph confirms it so the caller doesn't hand out a code that falls
        # through to the Entra password prompt.
        tap_id = (result or {}).get("id")
        if tap_id:
            self._wait_until_tap_usable(token, tap_path, tap_id)

        return tap

    def _wait_until_tap_usable(
        self, token: str, tap_path: str, tap_id: str, timeout_s: int = 30
    ) -> None:
        """Poll Graph until the TAP reports ``isUsable``, up to ``timeout_s`` seconds.

        Some tenants omit ``isUsable`` from the response; treated as usable
        rather than blocked on, since this is a best-effort wait, not a gate.
        """
        deadline = time.monotonic() + timeout_s
        while True:
            method = self._graph(token, "GET", f"{tap_path}/{tap_id}")
            if method is None or "isUsable" not in method or method.get("isUsable"):
                return
            if time.monotonic() >= deadline:
                return
            time.sleep(2)

    @keyword
    def delete_tap_for_user(self, user_upn: str) -> None:
        """Delete any Temporary Access Pass currently active for *user_upn*.

        No-op if the user has none. Call this from a test teardown so a TAP
        never lingers on a shared account and gets picked up by unrelated
        passwordless probes. Requires ``AUTHD_MSENTRAID_ISSUER_ID``,
        ``AUTHD_MSENTRAID_CLIENT_ID``, and ``AUTHD_MSENTRAID_CLIENT_SECRET``
        to be set.
        """
        issuer = os.environ["AUTHD_MSENTRAID_ISSUER_ID"]
        client_id = os.environ["AUTHD_MSENTRAID_CLIENT_ID"]
        client_secret = os.environ["AUTHD_MSENTRAID_CLIENT_SECRET"]
        tenant_id = self._tenant_id_from_issuer(issuer)

        token = self._acquire_token(tenant_id, client_id, client_secret)

        tap_path = f"/users/{user_upn}/authentication/temporaryAccessPassMethods"
        existing = self._graph(token, "GET", tap_path)
        for method in (existing or {}).get("value", []):
            tap_id = method.get("id")
            if tap_id:
                self._graph(token, "DELETE", f"{tap_path}/{tap_id}")
