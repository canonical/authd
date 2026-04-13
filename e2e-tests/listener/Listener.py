import sys
import time
from datetime import timedelta
from pathlib import Path

from robot import result, running
from robot.api.interfaces import ListenerV3

# Write directly to the real stderr so output bypasses Robot Framework's
# stdout capture and also isn't swallowed by the `grep -v` pipe in run-tests.sh
# (which only filters stdout).
_out = sys.__stderr__

# ANSI colour codes
_RESET  = "\033[0m"
_BOLD   = "\033[1m"
_DIM    = "\033[2m"
_GREEN  = "\033[32m"
_RED    = "\033[31m"
_YELLOW = "\033[33m"
_CYAN   = "\033[36m"
_WHITE  = "\033[37m"

# Log level colours (matching Robot Framework conventions)
_LEVEL_COLOUR = {
    "TRACE": _DIM,
    "DEBUG": _DIM,
    "INFO":  "",
    "WARN":  _YELLOW,
    "ERROR": _RED,
    "FAIL":  _RED,
}

SILENT_KEYWORDS = [
    # Dumping the whole journal on stderr is too noisy. It's already included
    # in the HTML log and stored separately as a .journal file.
    "Journal.Log Journal",
    # We stream the output of the browser login script continuously to stderr,
    # and after the script finishes we log the collected output to the
    # Robot Framework log file. To avoid duplication, we mark the login keyword
    # as silent so its log messages are not printed to stderr.
    "Browser.Login",
]

def _write(text: str) -> None:
    _out.write(text + "\n")
    _out.flush()


def _elapsed(seconds: float) -> str:
    td = timedelta(seconds=round(seconds))
    h, rem = divmod(int(td.total_seconds()), 3600)
    m, s = divmod(rem, 60)
    if h:
        return f"{h}h{m:02d}m{s:02d}s"
    if m:
        return f"{m}m{s:02d}s"
    return f"{s}s"


def _status_colour(status: str) -> str:
    return {
        "PASS": _GREEN,
        "FAIL": _RED,
        "SKIP": _YELLOW,
    }.get(status, _WHITE)


def _fmt_args(args: tuple) -> str:
    """Return a compact, single-line representation of keyword arguments."""
    if not args:
        return ""
    parts = []
    for a in args:
        s = str(a)
        if len(s) > 60:
            s = s[:57] + "..."
        parts.append(s)
    joined = "  ".join(parts)
    if len(joined) > 120:
        joined = joined[:117] + "..."
    return f"  {_DIM}{joined}{_RESET}"


class Listener(ListenerV3):
    """Progress listener: prints suite/test/keyword/log events with timing."""

    def __init__(self) -> None:
        self._suite_start: float = 0.0
        self._test_start: float = 0.0
        # Stack of (name, start_time) for keywords so we can track depth
        self._kw_stack: list[tuple[str, float]] = []
        self.silent = False

    # ------------------------------------------------------------------
    # Suite
    # ------------------------------------------------------------------

    def start_suite(self, data: running.TestSuite, result: result.TestSuite) -> None:
        self._suite_start = time.monotonic()
        _write(f"\n{_BOLD}{_CYAN}{'━' * 72}{_RESET}")
        _write(f"{_BOLD}{_CYAN}Suite  {data.name}{_RESET}")
        _write(f"{_BOLD}{_CYAN}{'━' * 72}{_RESET}")

    def end_suite(self, data: running.TestSuite, result: result.TestSuite) -> None:
        elapsed = time.monotonic() - self._suite_start
        colour = _status_colour(result.status)
        _write(
            f"{_BOLD}{_CYAN}{'━' * 72}{_RESET}\n"
            f"{_BOLD}{_CYAN}Suite  {data.name}  "
            f"{colour}{result.status}{_RESET}  "
            f"{_DIM}{_elapsed(elapsed)}{_RESET}  "
            f"{_DIM}{result.stat_message}{_RESET}\n"
        )

    # ------------------------------------------------------------------
    # Test
    # ------------------------------------------------------------------

    def start_test(self, data: running.TestCase, result: result.TestCase) -> None:
        self._test_start = time.monotonic()
        self._kw_stack.clear()
        _write(f"\n{_BOLD}▶  {data.name}{_RESET}")

    def end_test(self, data: running.TestCase, result: result.TestCase) -> None:
        elapsed = time.monotonic() - self._test_start
        colour = _status_colour(result.status)
        msg = f"  {_DIM}{result.message}{_RESET}" if result.message else ""
        _write(
            f"{colour}{_BOLD}{'PASS' if result.passed else result.status:4}{_RESET}"
            f"  {_BOLD}{data.name}{_RESET}"
            f"  {_DIM}{_elapsed(elapsed)}{_RESET}"
            f"{msg}"
        )

    # ------------------------------------------------------------------
    # Keywords  (user keywords + library keywords share start/end_keyword)
    # ------------------------------------------------------------------

    def start_keyword(
        self, data: running.Keyword, result: result.Keyword
    ) -> None:
        depth = len(self._kw_stack)
        self._kw_stack.append((data.name, time.monotonic()))

        silent_str = ""
        if data.name in SILENT_KEYWORDS:
            self.silent = True
            silent_str = f"  {_DIM}(silent){_RESET}"

        indent = "  " * (depth + 1)
        args_str = _fmt_args(data.args)
        _write(f"{indent}{_DIM}⋯  {data.name}{args_str}{silent_str}{_RESET}")

    def end_keyword(
        self, data: running.Keyword, result: result.Keyword
    ) -> None:
        if not self._kw_stack:
            return
        if data.name in SILENT_KEYWORDS:
            self.silent = False

        name, start = self._kw_stack.pop()
        depth = len(self._kw_stack)
        elapsed = time.monotonic() - start
        indent = "  " * (depth + 1)
        colour = _status_colour(result.status)
        args_str = _fmt_args(data.args)
        _write(
            f"{indent}{colour}{'✓' if result.passed else '✗'}  "
            f"{data.name}{args_str}"
            f"  {_DIM}{_elapsed(elapsed)}{_RESET}"
        )

    # ------------------------------------------------------------------
    # Log messages
    # ------------------------------------------------------------------

    def log_message(self, message: result.Message) -> None:
        if self.silent:
            return

        # Skip TRACE/DEBUG to avoid noise; show INFO and above.
        if message.level in ("TRACE", "DEBUG"):
            return
        # Indent to match the current keyword depth (same level as the keyword line).
        depth = len(self._kw_stack)
        indent = "  " * depth
        colour = _LEVEL_COLOUR.get(message.level, "")
        level_tag = f"[{message.level}] " if message.level not in ("INFO",) else ""
        # Strip HTML tags if the message is HTML
        text = message.message
        if message.html:
            import re
            text = re.sub(r"<[^>]+>", "", text)
        _write(f"{indent}{colour}{level_tag}{text}{_RESET}")

    # ------------------------------------------------------------------
    # Result files  (printed at the end, like --console verbose)
    # ------------------------------------------------------------------

    def output_file(self, path: Path) -> None:
        _write(f"{_DIM}Output:  {path}{_RESET}")

    def log_file(self, path: Path) -> None:
        _write(f"{_DIM}Log:     {path}{_RESET}")

    def report_file(self, path: Path) -> None:
        _write(f"{_DIM}Report:  {path}{_RESET}")
