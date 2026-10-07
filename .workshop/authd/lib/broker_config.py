# Update Workshop broker INI files without reformatting unrelated sections.
import ctypes
import datetime
import fcntl
import hashlib
import os
import re
import sys
import tempfile
from pathlib import Path

CREDENTIALS = {("oidc", key) for key in ("issuer", "client_id", "client_secret")}
FIELDS = CREDENTIALS | {
    ("users", "ssh_allowed_suffixes_first_auth"),
    ("users", "allowed_users"),
    ("msentraid", "register_device"),
    ("flows", "entra_auth"),
}
PLACEHOLDERS = ("<CLIENT_ID>", "<CLIENT_SECRET>", "<ISSUER_ID>", "<ISSUER_URL>")
ASSIGNMENT = re.compile(r'\s*(?:"([^"]+)"|`([^`]+)`|([^:=]+?))\s*[:=]\s*(.*)')
QUOTED = re.compile(r"""(['"])([^\n]*?)\1\s*(?:[#;].*)?$""")


class ConfigError(Exception):
    pass


class Config:
    def __init__(self, text):
        if "\0" in text:
            raise ConfigError("Unexpected NUL in broker config.")
        self.bom = "\ufeff" if text.startswith("\ufeff") else ""
        self.lines = text.removeprefix(self.bom).split("\n")
        self.lines = [line + "\n" for line in self.lines[:-1]] + (
            [self.lines[-1]] if self.lines[-1] else []
        )
        self.newline = "\r\n" if self.lines and self.lines[0].endswith("\r\n") else "\n"
        self.sections = {}
        self.entries = {}
        section = ""
        i = 0
        while i < len(self.lines):
            start = i
            line = self.lines[i].strip()
            i += 1
            if not line or line.startswith(("#", ";")):
                continue
            if line.startswith("[") and "]" in line:
                section = line[1 : line.rfind("]")]
                self.sections[section] = i
                continue
            match = ASSIGNMENT.fullmatch(line)
            if line.startswith("[") or not match:
                raise ConfigError(f"Invalid INI syntax on line {i}.")
            key = next(part for part in match.groups()[:3] if part is not None).strip()
            value = match[4]
            # Match the broker's ini.v1 quoting and continuation rules. Keep
            # the full line span so replacement can't leave continuation text.
            quote = next((q for q in ('"""', "`") if value.startswith(q)), None)
            if quote:
                value = value[len(quote) :]
                while quote not in value:
                    if i == len(self.lines):
                        raise ConfigError(f"Unclosed value on line {start + 1}.")
                    value += "\n" + self.lines[i].rstrip("\r\n")
                    i += 1
                value = value[: value.rfind(quote)]
            elif value.endswith("\\"):
                while value.endswith("\\") and i < len(self.lines):
                    value = value[:-1] + self.lines[i].strip()
                    i += 1
            elif match := QUOTED.fullmatch(value):
                value = match[2]
            else:
                value = re.split(r"[#;]", value, maxsplit=1)[0].rstrip()
            self.entries[section, key] = (start, i, value)

    def values(self):
        return {key: entry[2] for key, entry in self.entries.items()}

    def updated(self, updates):
        replacements = {}
        insertions = {}
        for (section, key), value in updates.items():
            # The broker accepts literal backtick-quoted strings, including
            # inline comment characters and a trailing backslash.
            if value and (
                value != value.strip()
                or value[0] in "'\"`"
                or value.endswith("\\")
                or any(c in value for c in "#;")
            ):
                value = f"`{value}`"
            line = f"{key} = {value}{self.newline}"
            if entry := self.entries.get((section, key)):
                start, end, _ = entry
                replacements[start] = end, line
            else:
                insertions.setdefault(section, []).append(line)
        for section, lines in insertions.items():
            if section in self.sections:
                start = self.sections[section]
                end, replacement = replacements.get(start, (start, ""))
                replacements[start] = end, "".join(lines) + replacement
        for section, lines in insertions.items():
            if section not in self.sections:
                start = len(self.lines)
                _, replacement = replacements.get(start, (start, ""))
                replacements[start] = (
                    start,
                    replacement
                    + f"{self.newline}[{section}]{self.newline}"
                    + "".join(lines),
                )
        for start, (end, text) in sorted(replacements.items(), reverse=True):
            if start and not self.lines[start - 1].endswith("\n"):
                text = self.newline + text
            self.lines[start:end] = [text]
        return self.bom + "".join(self.lines)


def complete(variant, values):
    required = (
        ("issuer", "client_id", "client_secret")
        if variant == "google"
        else ("issuer", "client_id")
    )
    for field in ("issuer", "client_id", "client_secret"):
        value = values.get(("oidc", field), "")
        if "\r" in value or "\n" in value or "\0" in value:
            raise ConfigError(f"{field} must be a single line.")
        if (field in required or value) and (
            not value.strip() or any(p in value for p in PLACEHOLDERS)
        ):
            return False
    return True


def merged_values(path, config):
    values = config.values()
    drop_ins = path.with_suffix(path.suffix + ".d")
    if drop_ins.exists():
        for drop_in in sorted(drop_ins.iterdir()):
            if not drop_in.is_dir():
                values.update(Config(drop_in.read_bytes().decode()).values())
    return values


def prepare(variant, path, template, updates):
    old = path.read_bytes() if path.exists() else None
    for field, value in updates.items():
        if (
            field not in FIELDS
            or any(c in value for c in "\r\n\0")
            or (field in CREDENTIALS and not value.strip())
        ):
            raise ConfigError(
                f"{field[1]} must be a valid single-line value"
                + (" and not empty." if field in CREDENTIALS else ".")
            )
    if old is None:
        seed = Config(template.read_bytes().decode())
        defaults = {
            key: ""
            for key, value in seed.values().items()
            if any(p in value for p in PLACEHOLDERS)
        }
        defaults["users", "allowed_users"] = "OWNER"
        defaults["users", "ssh_allowed_suffixes_first_auth"] = ""
        base = Config(seed.updated(defaults))
        was_complete = False
    else:
        base = Config(old.decode())
        was_complete = complete(variant, merged_values(path, base))
    candidate = base.updated(updates)
    config = Config(candidate)
    new_values = merged_values(path, config)
    for field in updates:
        if new_values.get(field) != config.entries[field][2]:
            raise ConfigError(
                f"{field[1]} is overridden by a broker.conf.d drop-in; update that file instead."
            )
    now_complete = complete(variant, new_values)
    if (was_complete or ("oidc", "client_id") in updates) and not now_complete:
        raise ConfigError(
            f"The resulting {variant} config needs an issuer and client ID"
            + (" and client secret." if variant == "google" else ".")
        )
    return old, candidate.encode(), was_complete, now_complete


def write_private(path, data, prefix):
    fd, name = tempfile.mkstemp(dir=path.parent, prefix=prefix)
    try:
        with os.fdopen(fd, "wb") as out:
            out.write(data)
        return Path(name)
    except BaseException:
        Path(name).unlink()
        raise


def commit(path, old, new):
    if new == old:
        return
    staged = write_private(path, new, f".{path.name}.")
    try:
        if old is not None:
            stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
            backup = write_private(path, old, f"{path.name}.bak-{stamp}-")
            print(f"Backup: {backup}", file=sys.stderr)
        os.replace(staged, path)
    finally:
        staged.unlink(missing_ok=True)


def secret_from_file(path):
    if not path.is_file():
        raise ConfigError(
            "Client secret must be a regular, readable file inside the workshop."
        )
    data = path.read_bytes().decode()
    data = (
        data.removesuffix("\r\n") if data.endswith("\r\n") else data.removesuffix("\n")
    )
    if not data.strip() or any(c in data for c in "\r\n\0"):
        raise ConfigError("Client secret must be one nonempty line.")
    return data


def replace_tree(src, dst):
    # Linux renameat2 exchanges complete sibling trees; Ubuntu's default mv
    # need not support --exchange. The caller removes the old tree at src.
    if os.path.lexists(dst):
        libc = ctypes.CDLL(None, use_errno=True)
        AT_FDCWD, RENAME_EXCHANGE = -100, 2
        if (
            libc.renameat2(
                AT_FDCWD, os.fsencode(src), AT_FDCWD, os.fsencode(dst), RENAME_EXCHANGE
            )
            != 0
        ):
            errno = ctypes.get_errno()
            raise OSError(errno, os.strerror(errno), dst)
    else:
        os.rename(src, dst)


def update(action, variant, path, extra, data):
    if action == "edit":
        old = path.read_bytes()
        if hashlib.sha256(old).hexdigest() != extra:
            raise ConfigError(
                "Broker config changed while the editor was open; reopen it and retry."
            )
        if data == old:
            print("unchanged")
            return
        if not complete(variant, merged_values(path, Config(data.decode()))):
            raise ConfigError("The edited config is missing required OIDC credentials.")
        commit(path, old, data)
        print("updated")
    else:
        fields = iter(data.decode().split("\0")[:-1])
        updates = {
            tuple(name.split(".", 1)): value
            for name, value in zip(fields, fields, strict=True)
        }
        old, new, was_complete, now_complete = prepare(
            variant, path, Path(extra), updates
        )
        if action == "configure":
            commit(path, old, new)
        print(int(was_complete), int(now_complete))


def main(args):
    action, *args = args
    if action == "secret-file":
        sys.stdout.write(secret_from_file(Path(args[0])))
    elif action == "dbus-name":
        name = (
            Config(Path(args[0]).read_bytes().decode())
            .values()
            .get(("authd", "dbus_name"), "")
        )
        if not re.fullmatch(
            r"[A-Za-z_-][A-Za-z0-9_-]*(?:\.[A-Za-z_-][A-Za-z0-9_-]*)+", name
        ):
            raise ConfigError("Missing or invalid dbus_name in broker registration.")
        print(name)
    elif action == "replace-tree":
        replace_tree(*args)
    else:
        variant, filename, extra = args
        path = Path(filename)
        data = sys.stdin.buffer.read()
        if action == "plan":
            update(action, variant, path, extra, data)
        else:
            fd = os.open(path.parent, os.O_DIRECTORY)
            try:
                fcntl.flock(fd, fcntl.LOCK_EX)
                update(action, variant, path, extra, data)
            finally:
                os.close(fd)


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (ConfigError, OSError, UnicodeError, ValueError) as exc:
        if isinstance(exc, (UnicodeError, ValueError)):
            sys.exit("Invalid broker config encoding or input.")
        sys.exit(str(exc))
