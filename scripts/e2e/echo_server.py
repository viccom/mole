#!/usr/bin/env python3
"""E2E 回声服务：客户端侧目标端口，经隧道往返验证数据面"""
import socket
import threading

def handle(conn):
    try:
        while True:
            data = conn.recv(4096)
            if not data:
                break
            conn.sendall(data)
    except Exception:
        pass
    finally:
        conn.close()

s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 15999))
s.listen(16)
print("echo server on 127.0.0.1:15999", flush=True)
while True:
    c, _ = s.accept()
    threading.Thread(target=handle, args=(c,), daemon=True).start()
