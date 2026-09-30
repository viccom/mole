#!/usr/bin/env python3
"""staging HTTPS 文件服务（自更新冒烟用）——服务 ~/staging，端口 443。

用法（WSL）：
  1) 准备物料：~/staging/app/molec/{latest.json, moleagent-client-*}
  2) 自签证书：~/staging/keys/{cert.pem,key.pem}，CN/SAN=fs.px.metme.top，
     并 /etc/hosts 指向 127.0.0.1
  3) python3 staging_server.py
  4) 被测客户端须 NO_PROXY=fs.px.metme.top 运行（否则代理会让它取到真实生产清单）
"""
import http.server
import os
import ssl

ROOT = os.path.expanduser("~/staging")


class Rooted(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **kw):
        super().__init__(*a, directory=ROOT, **kw)


httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 443), Rooted)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(os.path.join(ROOT, "keys/cert.pem"), os.path.join(ROOT, "keys/key.pem"))
httpd.socket = ctx.wrap_socket(httpd.socket, server_side=True)
print("staging https server on :443, root:", ROOT, flush=True)
httpd.serve_forever()
