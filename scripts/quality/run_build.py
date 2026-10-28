#!/usr/bin/env python3
"""Build to fresh, retained directories and require nonempty output files."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
PNPM = os.environ.get('NERAE_PNPM', 'pnpm')
BUILDS = {
    'client': [('h5', 'index.html'), ('mp-weixin', 'app.js'), ('app', 'app-service.js')],
    'admin': [('admin', 'index.html')],
}


def main() -> int:
    if len(sys.argv) != 2 or sys.argv[1] not in BUILDS:
        print('usage: run_build.py client|admin', file=sys.stderr)
        return 2
    base = os.environ.get('NERAE_BUILD_ROOT')
    try:
        directory = Path(tempfile.mkdtemp(prefix='nerae-foundation-build-', dir=base))
    except OSError as error:
        print(f'cannot create build directory: {error}', file=sys.stderr)
        return 1
    print(f'Fresh build output retained at {directory}', flush=True)
    for platform, required in BUILDS[sys.argv[1]]:
        output = directory / platform / 'output'
        env = {**os.environ}
        env.pop('UNI_OUTPUT_DIR', None)
        if platform == 'admin':
            command = [PNPM, '--filter', '@nerae/admin', 'run', 'build', '--outDir', str(output)]
        else:
            env['UNI_OUTPUT_DIR'] = str(output)
            command = [PNPM, '--filter', '@nerae/client', 'run', f'build:{platform}']
        try:
            status = subprocess.run(command, cwd=ROOT, env=env, check=False).returncode
        except OSError as error:
            print(f'cannot run build: {error}', file=sys.stderr)
            return 1
        artifact = output / required
        if status or artifact.is_symlink() or not artifact.is_file() or artifact.stat().st_size == 0:
            print(f'build {platform} failed or did not generate nonempty {required}', file=sys.stderr)
            return 1
        print(f'{platform}: {artifact.stat().st_size} bytes at {artifact}', flush=True)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
