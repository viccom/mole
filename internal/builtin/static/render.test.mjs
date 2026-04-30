import test from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tagName, ownerDocument) {
    this.tagName = tagName.toUpperCase();
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.parentNode = null;
    this.dataset = {};
    this.className = '';
    this.innerHTML = '';
    this.textContent = '';
  }

  appendChild(child) {
    if (child.parentNode) {
      child.parentNode.removeChild(child);
    }
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  insertBefore(child, beforeChild) {
    if (!beforeChild) {
      return this.appendChild(child);
    }
    if (child.parentNode) {
      child.parentNode.removeChild(child);
    }
    const index = this.children.indexOf(beforeChild);
    if (index === -1) {
      return this.appendChild(child);
    }
    child.parentNode = this;
    this.children.splice(index, 0, child);
    return child;
  }

  removeChild(child) {
    const index = this.children.indexOf(child);
    if (index === -1) {
      throw new Error('child not found');
    }
    this.children.splice(index, 1);
    child.parentNode = null;
    return child;
  }

  remove() {
    if (this.parentNode) {
      this.parentNode.removeChild(this);
    }
  }
}

class FakeDocument {
  createElement(tagName) {
    return new FakeElement(tagName, this);
  }
}

function tunnel(name, overrides = {}) {
  return {
    name,
    type: 'http',
    target: '127.0.0.1:8080',
    connected: false,
    bytes_in: 0,
    bytes_out: 0,
    ...overrides,
  };
}

test('syncTunnelTable reuses keyed rows across refreshes', async () => {
  const { syncTunnelTable } = await import('./render.js?case=reuse');
  const doc = new FakeDocument();
  const tbody = doc.createElement('tbody');

  syncTunnelTable(tbody, [
    tunnel('alpha', { connected: true }),
    tunnel('beta', { type: 'ser2mq' }),
  ], 'alpha');

  const firstAlpha = tbody.children[0];
  const firstBeta = tbody.children[1];

  syncTunnelTable(tbody, [
    tunnel('alpha', { connected: true, bytes_in: 128 }),
    tunnel('beta', { type: 'ser2mq', bytes_out: 512 }),
  ], 'beta');

  assert.equal(tbody.children.length, 2);
  assert.equal(tbody.children[0], firstAlpha);
  assert.equal(tbody.children[1], firstBeta);
  assert.equal(tbody.children[0].className, '');
  assert.equal(tbody.children[1].className, 'selected');
  assert.match(tbody.children[0].innerHTML, /128 B/);
  assert.match(tbody.children[1].innerHTML, /512 B/);
});

test('syncTunnelTable toggles empty state without leaving stale rows', async () => {
  const { syncTunnelTable } = await import('./render.js?case=empty');
  const doc = new FakeDocument();
  const tbody = doc.createElement('tbody');

  syncTunnelTable(tbody, [], null);
  assert.equal(tbody.children.length, 1);
  assert.equal(tbody.children[0].dataset.empty, 'true');
  assert.match(tbody.children[0].innerHTML, /暂无隧道/);

  syncTunnelTable(tbody, [tunnel('gamma', { type: 'vpn-manager' })], 'gamma');
  assert.equal(tbody.children.length, 1);
  assert.equal(tbody.children[0].dataset.tunnelName, 'gamma');
  assert.equal(tbody.children[0].dataset.empty, undefined);
  assert.equal(tbody.children[0].className, 'selected');
  assert.match(tbody.children[0].innerHTML, /详情/);
});
