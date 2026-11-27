"""Receive and discard TCP streams, for the Wi-Fi upload tests.

Run on a wired machine on the same network: python3 tcp-sink.py [PORT]
"""

import socket
import sys
import threading


def drain(conn: socket.socket) -> None:
    with conn:
        while conn.recv(1 << 20):
            pass


def main() -> None:
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 19999
    srv = socket.socket()
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("0.0.0.0", port))  # noqa: S104 - must be reachable from the board
    srv.listen(8)
    print(f"discarding TCP on port {port}", flush=True)
    while True:
        conn, _ = srv.accept()
        threading.Thread(target=drain, args=(conn,), daemon=True).start()


if __name__ == "__main__":
    main()
