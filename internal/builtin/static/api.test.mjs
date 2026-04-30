import test from 'node:test';
import assert from 'node:assert/strict';

test('request throws readable error for non-json failure responses', async () => {
  globalThis.fetch = async () => ({
    ok: false,
    status: 502,
    headers: new Headers({ 'content-type': 'text/plain' }),
    text: async () => 'bad gateway',
  });

  const { api } = await import('./api.js?case=nonjson');

  await assert.rejects(
    () => api.getStatus(),
    /502.*bad gateway/i
  );
});

test('request aborts hung requests after timeout', async () => {
  let aborted = false;
  globalThis.fetch = (_url, opts) => new Promise((_resolve, reject) => {
    opts.signal.addEventListener('abort', () => {
      aborted = true;
      reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
    });
  });

  const { request } = await import('./api.js?case=timeout');

  await assert.rejects(
    () => request('GET', '/api/status', undefined, { timeoutMs: 5 }),
    /timed out/i
  );
  assert.equal(aborted, true);
});
