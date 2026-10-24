"""QA environment: serve a frozen build on loopback and return 404 for raster images.

Does not alter source, output files, browser state, or application JavaScript.
Copy and hash a completed frozen dist before using this server. Stop it after QA.
"""
import argparse
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit


class MissingImageHandler(SimpleHTTPRequestHandler):
    def do_GET(self):
        path = urlsplit(self.path).path
        if Path(path).suffix.lower() in {'.jpg', '.jpeg', '.png', '.webp', '.gif', '.avif'}:
            self.send_error(404, 'Deliberate missing-image QA response')
            return
        super().do_GET()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    parser.add_argument('--port', type=int, default=0)
    args = parser.parse_args()
    directory = args.directory.resolve()
    if not (directory / 'index.html').is_file():
        parser.error('directory must contain a completed build index.html')
    server = ThreadingHTTPServer(
        ('127.0.0.1', args.port), partial(MissingImageHandler, directory=str(directory)))
    print(f'http://127.0.0.1:{server.server_port}/', flush=True)
    try:
        server.serve_forever()
    finally:
        server.server_close()


if __name__ == '__main__':
    main()
