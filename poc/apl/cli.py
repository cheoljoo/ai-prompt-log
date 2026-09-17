"""Entry point: `apl` launches the TUI, `apl sync` mirrors logs into the
current aggregate project's cache directory (see plan.md §2-1)."""
from __future__ import annotations

import sys


def main() -> None:
    if len(sys.argv) > 1 and sys.argv[1] == "sync":
        from .sync import run_sync

        run_sync()
        return
    from .tui import AplApp

    AplApp().run()


if __name__ == "__main__":
    main()
