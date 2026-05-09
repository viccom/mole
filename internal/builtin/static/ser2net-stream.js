// ser2net-stream.js — SSE 实时流客户端（Ser2Net）
import { buildTunnelStreamURL } from './api.js';

let currentSource = null;

export function openSer2NetStream(name, handlers = {}, options = {}) {
  closeSer2NetStream();

  const url = buildTunnelStreamURL(name, options);
  const source = new EventSource(url);
  currentSource = source;

  source.addEventListener('packet', (e) => {
    try {
      const evt = JSON.parse(e.data);
      if (handlers.onPacket) handlers.onPacket(evt);
    } catch (_) {}
  });

  source.addEventListener('error', () => {
    if (handlers.onError) handlers.onError(new Error('SSE connection error'));
  });

  return source;
}

export function closeSer2NetStream() {
  if (currentSource) {
    currentSource.close();
    currentSource = null;
  }
}
