import { useEffect, useRef, useState, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { getToken } from '../api/client'
import { FilePanel } from '../components/FilePanel'

// 右键菜单组件
function ContextMenu({ x, y, onCopy, onPaste, onClose }: { x: number; y: number; onCopy: () => void; onPaste: () => void; onClose: () => void }) {
  return (
    <div
      className="fixed z-[100] bg-gray-800 border border-gray-600 rounded-lg shadow-xl py-1 min-w-[120px]"
      style={{ left: x, top: y }}
      onMouseLeave={onClose}
    >
      <button onClick={() => { onCopy(); onClose() }} className="w-full px-4 py-1.5 text-sm text-gray-200 hover:bg-gray-600 text-left">
        复制
      </button>
      <button onClick={() => { onPaste(); onClose() }} className="w-full px-4 py-1.5 text-sm text-gray-200 hover:bg-gray-600 text-left">
        粘贴
      </button>
    </div>
  )
}

// 消息类型（与客户端/服务端一致）
const MSG_DATA = 0x01
const MSG_RESIZE = 0x02
const MSG_KEEP = 0x03

// 文件操作响应
const MSG_FILE_LIST_RESP = 0x0C
const MSG_FILE_DATA_RESP = 0x0D
const MSG_FILE_ACK_RESP = 0x0E

export function WebSSHPage() {
  const { tunnelName } = useParams<{ tunnelName: string }>()
  const navigate = useNavigate()
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const fitRef = useRef<FitAddon | null>(null)

  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected' | 'error'>('connecting')
  const [errorMsg, setErrorMsg] = useState('')
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number } | null>(null)
  const [showFilePanel, setShowFilePanel] = useState(false)
  const fileMsgHandlerRef = useRef<((msgType: number, payload: Uint8Array) => void) | null>(null)

  const cleanup = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }
    if (termRef.current) {
      termRef.current.dispose()
      termRef.current = null
    }
  }, [])

  const handleCopy = useCallback(() => {
    const term = termRef.current
    if (term && term.hasSelection()) {
      navigator.clipboard.writeText(term.getSelection())
    }
  }, [])

  const handlePaste = useCallback(async () => {
    const text = await navigator.clipboard.readText()
    const ws = wsRef.current
    if (text && ws?.readyState === WebSocket.OPEN) {
      const encoder = new TextEncoder()
      const payload = encoder.encode(text)
      const msg = new Uint8Array(1 + payload.length)
      msg[0] = MSG_DATA
      msg.set(payload, 1)
      ws.send(msg)
    }
  }, [])

  useEffect(() => {
    if (!tunnelName || !containerRef.current) return

    const token = getToken()
    if (!token) {
      setStatus('error')
      setErrorMsg('未登录，请先登录')
      return
    }

    // 创建终端
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, Consolas, "Courier New", monospace',
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
        cursor: '#d4d4d4',
        selectionBackground: '#264f78',
      },
      smoothScrollDuration: 0,
      fastScrollSensitivity: 5,
    })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(containerRef.current)
    fitAddon.fit()

    termRef.current = term
    fitRef.current = fitAddon

    // 建立 WebSocket 连接
    const protocol = location.protocol === 'https:' ? 'wss' : 'ws'
    const wsUrl = `${protocol}://${location.host}/api/v1/tunnels/${encodeURIComponent(tunnelName)}/webssh?token=${encodeURIComponent(token)}`
    const ws = new WebSocket(wsUrl)
    ws.binaryType = 'arraybuffer'
    wsRef.current = ws

    ws.onopen = () => {
      setStatus('connected')
      term.focus()

      // 发送初始窗口大小
      sendResize(term.cols, term.rows)
    }

    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) {
        const buf = new Uint8Array(ev.data)
        if (buf.length === 0) return
        const msgType = buf[0]
        const payload = buf.slice(1)

        switch (msgType) {
        case MSG_DATA:
          term.write(new TextDecoder('utf-8').decode(payload))
          break
        case MSG_FILE_LIST_RESP:
        case MSG_FILE_DATA_RESP:
        case MSG_FILE_ACK_RESP:
          if (fileMsgHandlerRef.current) {
            fileMsgHandlerRef.current(msgType, payload)
          }
          break
        }
      }
    }

    ws.onerror = () => {
      setStatus('error')
      setErrorMsg('WebSocket 连接错误')
    }

    ws.onclose = (ev) => {
      setStatus('disconnected')
      if (ev.code !== 1000) {
        term.writeln('\r\n\x1b[31m连接已断开\x1b[0m')
      }
    }

    // 终端输入 → WebSocket
    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        const encoder = new TextEncoder()
        const payload = encoder.encode(data)
        const msg = new Uint8Array(1 + payload.length)
        msg[0] = MSG_DATA
        msg.set(payload, 1)
        ws.send(msg)
      }
    })

    // 窗口大小变化
    const onResize = () => {
      fitAddon.fit()
      sendResize(term.cols, term.rows)
    }

    const sendResize = (cols: number, rows: number) => {
      if (ws.readyState === WebSocket.OPEN) {
        const payload = new TextEncoder().encode(JSON.stringify({ cols, rows }))
        const msg = new Uint8Array(1 + payload.length)
        msg[0] = MSG_RESIZE
        msg.set(payload, 1)
        ws.send(msg)
      }
    }

    window.addEventListener('resize', onResize)

    // 右键菜单
    const onContextMenu = (e: MouseEvent) => {
      e.preventDefault()
      setContextMenu({ x: e.clientX, y: e.clientY })
    }
    containerRef.current.addEventListener('contextmenu', onContextMenu)

    // Ctrl+Shift+C/V 快捷键
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.shiftKey && e.key === 'C') {
        e.preventDefault()
        if (term.hasSelection()) {
          navigator.clipboard.writeText(term.getSelection())
        }
      }
      if (e.ctrlKey && e.shiftKey && e.key === 'V') {
        e.preventDefault()
        navigator.clipboard.readText().then(text => {
          if (text && ws.readyState === WebSocket.OPEN) {
            const encoder = new TextEncoder()
            const payload = encoder.encode(text)
            const msg = new Uint8Array(1 + payload.length)
            msg[0] = MSG_DATA
            msg.set(payload, 1)
            ws.send(msg)
          }
        })
      }
    }
    window.addEventListener('keydown', onKeyDown)

    // 心跳
    const heartbeat = setInterval(() => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new Uint8Array([MSG_KEEP]))
      }
    }, 30000)

    return () => {
      window.removeEventListener('resize', onResize)
      window.removeEventListener('keydown', onKeyDown)
      containerRef.current?.removeEventListener('contextmenu', onContextMenu)
      clearInterval(heartbeat)
      cleanup()
    }
  }, [tunnelName, cleanup])

  const registerFileHandler = useCallback((handler: ((msgType: number, payload: Uint8Array) => void) | null) => {
    fileMsgHandlerRef.current = handler
  }, [])

  // 文件面板开关时重新 fit 终端
  useEffect(() => {
    const timer = setTimeout(() => {
      if (fitRef.current && termRef.current) {
        fitRef.current.fit()
      }
    }, 50)
    return () => clearTimeout(timer)
  }, [showFilePanel])

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-[#1e1e1e]" onClick={() => setContextMenu(null)}>
      {/* 顶部状态栏 */}
      <div className="flex items-center justify-between px-4 py-2 bg-gray-900 text-white text-sm shrink-0">
        <div className="flex items-center gap-3">
          <button
            onClick={() => navigate(-1)}
            className="px-2 py-1 rounded hover:bg-gray-700 transition-colors"
            title="返回"
          >
            ← 返回
          </button>
          <span className="text-gray-400">|</span>
          <span className="font-mono">{tunnelName}</span>
          <span className={`inline-block w-2 h-2 rounded-full ${
            status === 'connected' ? 'bg-green-400' :
            status === 'connecting' ? 'bg-yellow-400 animate-pulse' :
            'bg-red-400'
          }`} />
          <span className="text-gray-400 text-xs">
            {status === 'connecting' ? '连接中...' :
             status === 'connected' ? '已连接' :
             status === 'disconnected' ? '已断开' :
             errorMsg || '错误'}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {status === 'connected' && (
            <>
              <button
                onClick={() => setShowFilePanel(v => !v)}
                className={`px-2 py-1 rounded transition-colors text-xs ${showFilePanel ? 'bg-blue-600 hover:bg-blue-500' : 'bg-gray-700 hover:bg-gray-600'}`}
                title="文件管理"
              >
                📁 文件
              </button>
              <button
                onClick={handleCopy}
                className="px-2 py-1 rounded bg-gray-700 hover:bg-gray-600 transition-colors text-xs"
                title="复制 (Ctrl+Shift+C)"
              >
                复制
              </button>
              <button
                onClick={handlePaste}
                className="px-2 py-1 rounded bg-gray-700 hover:bg-gray-600 transition-colors text-xs"
                title="粘贴 (Ctrl+Shift+V)"
              >
                粘贴
              </button>
            </>
          )}
          {status === 'disconnected' && (
            <button
              onClick={() => window.location.reload()}
              className="px-2 py-1 rounded bg-gray-700 hover:bg-gray-600 transition-colors text-xs"
            >
              重新连接
            </button>
          )}
        </div>
      </div>

      {/* 终端 + 文件面板 */}
      <div className="flex flex-1 min-h-0">
        <div ref={containerRef} className="flex-1 overflow-hidden p-1" />
        {showFilePanel && status === 'connected' && (
          <div className="w-72 border-l border-gray-700 bg-gray-800 flex flex-col shrink-0">
            <FilePanel ws={wsRef.current} onRegister={registerFileHandler} />
          </div>
        )}
      </div>

      {/* 右键菜单 */}
      {contextMenu && (
        <ContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          onCopy={handleCopy}
          onPaste={handlePaste}
          onClose={() => setContextMenu(null)}
        />
      )}
    </div>
  )
}
