#!/usr/bin/env python3
"""通道加密 E2E 中间人代理（方案 B 场景 sc22 / sc23）。

用法:
  python3 enc_proxy.py --listen 127.0.0.1:19881 --upstream 127.0.0.1:9981 --mode strip
  python3 enc_proxy.py --listen 127.0.0.1:19881 --upstream 127.0.0.1:9981 --mode corrupt

模式:
  strip   演示过渡期残余风险（B.1 已采纳决策）：客户端→服务端方向的首行
          （认证行，以 \\n 结尾的 JSON）解析后删除 "enc" 键再重组转发——
          服务端视为旧客户端，ok 应答不带 enc → 新客户端回落明文（WARN 一次）。
  corrupt 演示「握手失败绝不回落」安全边界：服务端→客户端方向 32 字节
          challenge 与 ok 行原样转发后，读第一帧 Noise 消息（msg2，
          2 字节大端长度前缀 + 体），把体首字节按 0xA5 异或翻转若干比特再
          转发——客户端 AEAD 校验必败，触发专属错误串并断开重连，绝不回落明文。

其余字节一律双向原样透传。每个动作写一行日志到 stderr（harness 重定向到
proxy.log，作为「MITM 路径确实生效」的断言锚点，防止代理空转造成假绿）。
"""
import argparse
import json
import socket
import sys
import threading
import time

CHALLENGE_LEN = 32


def log(conn_id, msg):
    sys.stderr.write("[proxy %s] %s %s\n" % (conn_id, time.strftime("%H:%M:%S"), msg))
    sys.stderr.flush()


def read_exact(sock, n, conn_id, what):
    """读满 n 字节；EOF/超时抛异常（由调用方统一清理）。"""
    buf = b""
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise EOFError("eof reading %s (%d/%d)" % (what, len(buf), n))
        buf += chunk
    return buf


def read_line(sock, conn_id):
    """读到第一个 \\n（含）为止，返回原始行字节。行内保证无换行（认证行契约）。"""
    buf = b""
    while not buf.endswith(b"\n"):
        chunk = sock.recv(4096)
        if not chunk:
            raise EOFError("eof reading line (%d bytes so far)" % len(buf))
        buf += chunk
        if len(buf) > 65536:
            raise ValueError("line exceeds 64KB without newline")
    return buf


def pump(src, dst, conn_id, tag):
    """原样转发直到任一方向断开。"""
    try:
        while True:
            data = src.recv(65536)
            if not data:
                break
            dst.sendall(data)
    except OSError:
        pass
    finally:
        log(conn_id, "%s direction closed" % tag)
        try:
            src.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        try:
            dst.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass


def c2s_strip(client, upstream, conn_id):
    """客户端→服务端：首行（认证行）删 enc 键后转发，其余原样。"""
    try:
        line = read_line(client, conn_id)
        out = line
        note = "unparseable, forwarded verbatim"
        try:
            obj = json.loads(line.decode("utf-8"))
            if isinstance(obj, dict) and "enc" in obj:
                del obj["enc"]
                # 行内无换行（认证行契约），重组安全：紧凑序列化 + 换行
                out = json.dumps(obj, separators=(",", ":")).encode("utf-8") + b"\n"
                note = "stripped enc field"
            else:
                note = "no enc field present (old client?), forwarded verbatim"
        except (ValueError, UnicodeDecodeError):
            pass
        upstream.sendall(out)
        log(conn_id, "auth line: %s (%d -> %d bytes)" % (note, len(line), len(out)))
        pump(client, upstream, conn_id, "c2s")
    except (EOFError, ValueError, OSError) as exc:
        log(conn_id, "c2s_strip ended: %r" % (exc,))


def s2c_corrupt(server, client, conn_id):
    """服务端→客户端：challenge 与 ok 行原样转发，随后破坏第一帧 Noise 消息。"""
    try:
        challenge = read_exact(server, CHALLENGE_LEN, conn_id, "challenge")
        client.sendall(challenge)
        log(conn_id, "challenge forwarded verbatim (%d bytes)" % len(challenge))

        ok_line = read_line(server, conn_id)
        client.sendall(ok_line)
        log(conn_id, "ok line forwarded verbatim (%d bytes)" % len(ok_line))

        hdr = read_exact(server, 2, conn_id, "noise frame header")
        n = (hdr[0] << 8) | hdr[1]
        if n == 0:
            client.sendall(hdr)
            log(conn_id, "first noise frame empty, forwarded unchanged")
        else:
            body = bytearray(read_exact(server, n, conn_id, "noise frame body"))
            body[0] ^= 0xA5  # 翻转首字节 1 个比特——AEAD 校验必败
            client.sendall(hdr + bytes(body))
            log(conn_id, "corrupted first noise frame (msg2): body[0] ^= 0xA5, len=%d" % n)
        pump(server, client, conn_id, "s2c")
    except (EOFError, ValueError, OSError) as exc:
        log(conn_id, "s2c_corrupt ended: %r" % (exc,))


def handle(client, upstream_addr, mode, conn_id):
    client.settimeout(30)
    try:
        upstream = socket.create_connection(upstream_addr, timeout=10)
    except OSError as exc:
        log(conn_id, "upstream connect failed: %r" % (exc,))
        client.close()
        return
    upstream.settimeout(30)
    if mode == "strip":
        threading.Thread(target=c2s_strip, args=(client, upstream, conn_id), daemon=True).start()
        pump(upstream, client, conn_id, "s2c")  # 服务端→客户端原样
    else:  # corrupt
        threading.Thread(target=pump, args=(client, upstream, conn_id, "c2s"), daemon=True).start()
        s2c_corrupt(upstream, client, conn_id)
    upstream.close()
    client.close()
    log(conn_id, "connection closed")


def main():
    ap = argparse.ArgumentParser(description="mole channel-encryption E2E MITM proxy")
    ap.add_argument("--listen", default="127.0.0.1:19881", help="监听地址:端口")
    ap.add_argument("--upstream", default="127.0.0.1:9981", help="真实服务端地址:端口")
    ap.add_argument("--mode", required=True, choices=["strip", "corrupt"])
    args = ap.parse_args()

    listen_addr = tuple(args.listen.rsplit(":", 1))
    upstream_addr = tuple(args.upstream.rsplit(":", 1))
    upstream_addr = (upstream_addr[0], int(upstream_addr[1]))

    srv = socket.socket()
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind((listen_addr[0], int(listen_addr[1])))
    srv.listen(16)
    log("main", "listening on %s:%s -> upstream %s:%s mode=%s"
        % (listen_addr[0], listen_addr[1], upstream_addr[0], upstream_addr[1], args.mode))

    conn_seq = 0
    while True:
        conn, peer = srv.accept()
        conn_seq += 1
        cid = "%d/%s" % (conn_seq, peer[0])
        log(cid, "client connected")
        threading.Thread(target=handle, args=(conn, upstream_addr, args.mode, cid), daemon=True).start()


if __name__ == "__main__":
    main()
