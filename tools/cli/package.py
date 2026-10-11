#!/usr/bin/env python3
"""CI packaging helper only. The shipped CLI does not use Python or Node."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--executable', required=True)
    p.add_argument('--stage', required=True)
    p.add_argument('--browser-cache', required=True)
    p.add_argument('--archive', required=True)
    a = p.parse_args()

    stage = Path(a.stage).resolve()
    if stage.exists():
        shutil.rmtree(stage)
    stage.mkdir(parents=True)
    executable = Path(a.executable).resolve()
    assert executable.is_file(), executable
    shutil.copy2(executable, stage / executable.name)

    # Playwright installs a build of Chromium headless shell + all its resources.
    # Copy its *whole* directory; moving just the binary breaks font/render data.
    cache = Path(a.browser_cache).resolve()
    browsers = list(cache.glob('chromium_headless_shell-*'))
    candidates = []
    for browser in browsers:
        for name in ('chrome-headless-shell.exe', 'chrome-headless-shell'):
            candidates += [p for p in browser.rglob(name) if p.is_file()]
    if len(candidates) != 1:
        raise RuntimeError(f'Expected exactly one Chromium headless shell in {cache}, found {candidates}')
    chromium = candidates[0]
    chrome_dir = stage / 'runtime' / 'chrome'
    shutil.copytree(chromium.parent, chrome_dir)

    ffmpeg = Path(subprocess.check_output([
        'node', '-e', "process.stdout.write(require('ffmpeg-static'))"
    ], text=True).strip())
    assert ffmpeg.is_file(), ffmpeg
    ffmpeg_target = stage / 'runtime' / ffmpeg.name
    shutil.copy2(ffmpeg, ffmpeg_target)
    if os.name != 'nt':
        (chrome_dir / chromium.name).chmod(0o755)
        ffmpeg_target.chmod(0o755)

    (stage / 'THIRD_PARTY_NOTICES.txt').write_text('''JIZURA native CLI binary: MIT — https://github.com/852wa/JIZURA/blob/main/LICENSE
Embedded JIZURA browser code: MIT and listed notices — https://github.com/852wa/JIZURA/blob/main/THIRD_PARTY_NOTICES.md
Bundled Chromium headless shell: https://www.chromium.org/ and https://chromium.googlesource.com/chromium/src/+/main/LICENSE
Bundled FFmpeg via ffmpeg-static: https://ffmpeg.org/legal.html and https://github.com/eugeneware/ffmpeg-static
IMPORTANT: Bundled FFmpeg builds may enable GPL libraries. FFmpeg is distributed as a separate executable, not linked into the MIT Go binary. Review its build configuration and provide corresponding source/licensing notices before redistributing elsewhere.
Font selection uses your system's installed fonts when offline; fonts are not redistributed.
''', encoding='utf-8')
    archive = Path(a.archive).resolve()
    archive.parent.mkdir(parents=True, exist_ok=True)
    # Zip root has just the CLI plus the runtime directory, no extra nesting.
    shutil.make_archive(str(archive.with_suffix('')), 'zip', root_dir=stage)
    print('Package created:', archive, 'size:', archive.stat().st_size)


if __name__ == '__main__':
    main()
