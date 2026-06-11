import { useState, useRef, useCallback, useEffect } from 'react'

// 消息类型常量
const MSG_FILE_LIST_REQ = 0x04
const MSG_FILE_UPLOAD_REQ = 0x05
const MSG_FILE_UPLOAD_DATA = 0x06
const MSG_FILE_UPLOAD_END = 0x07
const MSG_FILE_DOWNLOAD_REQ = 0x08
const MSG_FILE_DELETE_REQ = 0x09
const MSG_FILE_MKDIR_REQ = 0x0A
const MSG_FILE_READ_REQ = 0x0B

const MSG_FILE_LIST_RESP = 0x0C
const MSG_FILE_DATA_RESP = 0x0D
const MSG_FILE_ACK_RESP = 0x0E
const MSG_FILE_READ_RESP = 0x0F

interface FileEntry {
  name: string
  size: number
  mode: string
  mod_time: string
  is_dir: boolean
}

interface UploadProgress {
  loaded: number
  total: number
  name: string
}

interface FilePanelProps {
  ws: WebSocket | null
  onRegister: (handler: ((msgType: number, payload: Uint8Array) => void) | null) => void
  onClose: () => void
}

const TEXT_EXTENSIONS = new Set([
  'txt', 'md', 'json', 'xml', 'yaml', 'yml', 'toml', 'ini', 'conf', 'cfg',
  'sh', 'bash', 'zsh', 'fish', 'py', 'rb', 'js', 'ts', 'jsx', 'tsx',
  'go', 'rs', 'java', 'c', 'h', 'cpp', 'hpp', 'cs', 'swift', 'kt',
  'html', 'htm', 'css', 'scss', 'less', 'sql', 'csv', 'log',
  'env', 'gitignore', 'dockerfile', 'makefile', 'cmake',
  'properties', 'env', 'service', 'socket', 'timer',
  'r', 'lua', 'perl', 'php', 'ex', 'exs', 'erl', 'hs', 'ml',
  'vue', 'svelte', 'astro', 'tf', 'hcl', 'nomad',
])

function isTextFile(name: string): boolean {
  const lower = name.toLowerCase()
  // 无扩展名的常见文本文件
  if (['makefile', 'dockerfile', 'vagrantfile', 'gemfile', 'rakefile', 'procfile', 'license', 'readme', 'changelog'].some(f => lower === f || lower.startsWith(f + '.'))) return true
  const dot = lower.lastIndexOf('.')
  if (dot < 0) return false
  const ext = lower.slice(dot + 1)
  return TEXT_EXTENSIONS.has(ext)
}

export function FilePanel({ ws, onRegister, onClose }: FilePanelProps) {
  const [files, setFiles] = useState<FileEntry[]>([])
  const [currentPath, setCurrentPath] = useState('/')
  const [homePath, setHomePath] = useState('/')
  const [error, setError] = useState('')
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<UploadProgress | null>(null)
  const [isFullscreen, setIsFullscreen] = useState(false)
  const [isDraggingOver, setIsDraggingOver] = useState(false)
  const [preview, setPreview] = useState<{ name: string; content: string; truncated: boolean } | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)

  // 面板位置和大小
  const [pos, setPos] = useState({ x: -1, y: -1 }) // -1 表示未初始化
  const [size, setSize] = useState({ w: 420, h: 500 })
  const dragRef = useRef<{ startX: number; startY: number; startPosX: number; startPosY: number } | null>(null)
  const resizeRef = useRef<{ startX: number; startY: number; startW: number; startH: number } | null>(null)

  const downloadChunks = useRef<Uint8Array[]>([])
  const downloading = useRef(false)
  const uploadingRef = useRef(false)

  // 首次渲染时居中
  useEffect(() => {
    if (pos.x === -1) {
      setPos({
        x: Math.max(20, window.innerWidth - size.w - 40),
        y: Math.max(60, Math.round((window.innerHeight - size.h) / 2)),
      })
    }
  }, [])

  const sendMsg = useCallback((type: number, payload: string | Uint8Array) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    const payloadBytes = typeof payload === 'string'
      ? new TextEncoder().encode(payload)
      : payload
    const msg = new Uint8Array(1 + payloadBytes.length)
    msg[0] = type
    msg.set(payloadBytes, 1)
    ws.send(msg)
  }, [ws])

  const getFileList = useCallback((path: string) => {
    setError('')
    sendMsg(MSG_FILE_LIST_REQ, JSON.stringify({ path }))
  }, [sendMsg])

  // 注册消息处理器
  useEffect(() => {
    const handler = (msgType: number, payload: Uint8Array) => {
      try {
        const text = () => new TextDecoder().decode(payload)

        switch (msgType) {
        case MSG_FILE_LIST_RESP: {
          const resp = JSON.parse(text())
          if (resp.files) setFiles(resp.files)
          if (resp.path) setCurrentPath(resp.path)
          if (resp.home) setHomePath(resp.home)
          setError('')
          break
        }
        case MSG_FILE_DATA_RESP: {
          downloadChunks.current.push(new Uint8Array(payload))
          break
        }
        case MSG_FILE_READ_RESP: {
          const resp = JSON.parse(text())
          setPreviewLoading(false)
          if (resp.ok) {
            setPreview({ name: resp.path?.split('/').pop() || '', content: resp.data, truncated: resp.truncated })
          } else {
            setError(resp.msg || 'read failed')
          }
          break
        }
        case MSG_FILE_ACK_RESP: {
          const ack = JSON.parse(text())
          if (ack.ok && downloading.current && downloadChunks.current.length > 0) {
            const parts = downloadChunks.current.map(c => new Uint8Array(c) as BlobPart)
            const blob = new Blob(parts)
            const url = URL.createObjectURL(blob)
            const a = document.createElement('a')
            a.href = url
            a.download = ack.name || 'download'
            document.body.appendChild(a)
            a.click()
            a.remove()
            URL.revokeObjectURL(url)
            downloadChunks.current = []
            downloading.current = false
          } else if (!ack.ok) {
            setError(ack.msg || 'operation failed')
            downloadChunks.current = []
            downloading.current = false
          }
          if (uploadingRef.current) {
            setUploading(false)
            setUploadProgress(null)
            uploadingRef.current = false
          }
          if (ack.ok) {
            getFileList(currentPath)
          }
          break
        }
        }
      } catch {
        setError('message parse error')
      }
    }
    onRegister(handler)
    return () => onRegister(null)
  }, [onRegister, currentPath, getFileList])

  useEffect(() => {
    getFileList('/')
  }, [getFileList])

  const navigateTo = (path: string) => {
    setCurrentPath(path)
    setPreview(null)
    getFileList(path)
  }

  const goUp = () => {
    if (currentPath === '/') return
    const parts = currentPath.split('/').filter(Boolean)
    parts.pop()
    navigateTo(parts.length === 0 ? '/' : '/' + parts.join('/'))
  }

  const handleRowClick = (file: FileEntry) => {
    if (file.is_dir) {
      const newPath = currentPath === '/' ? '/' + file.name : currentPath + '/' + file.name
      navigateTo(newPath)
    } else if (isTextFile(file.name)) {
      // 文本文件 → 预览
      const filePath = currentPath === '/' ? '/' + file.name : currentPath + '/' + file.name
      setPreviewLoading(true)
      sendMsg(MSG_FILE_READ_REQ, JSON.stringify({ path: filePath }))
    } else if (!downloading.current) {
      // 二进制文件 → 下载
      const filePath = currentPath === '/' ? '/' + file.name : currentPath + '/' + file.name
      downloadChunks.current = []
      downloading.current = true
      sendMsg(MSG_FILE_DOWNLOAD_REQ, JSON.stringify({ path: filePath }))
    }
  }

  const waitForBufferDrain = (socket: WebSocket, threshold: number): Promise<void> => {
    if (socket.bufferedAmount <= threshold) return Promise.resolve()
    return new Promise(resolve => {
      const check = () => {
        if (socket.bufferedAmount <= threshold || socket.readyState !== WebSocket.OPEN) {
          resolve()
        } else {
          setTimeout(check, 20)
        }
      }
      setTimeout(check, 20)
    })
  }

  const uploadFile = async (file: File) => {
    if (uploadingRef.current) return
    const filePath = currentPath === '/' ? '/' + file.name : currentPath + '/' + file.name
    setUploading(true)
    uploadingRef.current = true
    setUploadProgress({ loaded: 0, total: file.size, name: file.name })

    sendMsg(MSG_FILE_UPLOAD_REQ, JSON.stringify({ path: filePath, size: file.size }))

    const CHUNK = 32768
    const BUFFER_THRESHOLD = CHUNK * 4
    let offset = 0
    while (offset < file.size) {
      if (!ws || ws.readyState !== WebSocket.OPEN) break
      const end = Math.min(offset + CHUNK, file.size)
      const chunk = file.slice(offset, end)
      const buf = await chunk.arrayBuffer()
      if (ws.readyState !== WebSocket.OPEN) break
      await waitForBufferDrain(ws, BUFFER_THRESHOLD)
      const msg = new Uint8Array(1 + buf.byteLength)
      msg[0] = MSG_FILE_UPLOAD_DATA
      msg.set(new Uint8Array(buf), 1)
      ws.send(msg)
      offset = end
      setUploadProgress({ loaded: offset, total: file.size, name: file.name })
    }
    sendMsg(MSG_FILE_UPLOAD_END, JSON.stringify({ path: filePath }))
  }

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const fileList = e.target.files
    if (!fileList || fileList.length === 0) return
    await uploadFile(fileList[0])
    e.target.value = ''
  }

  const handleDelete = (file: FileEntry) => {
    if (!confirm(`确定删除 ${file.name}？`)) return
    const filePath = currentPath === '/' ? '/' + file.name : currentPath + '/' + file.name
    sendMsg(MSG_FILE_DELETE_REQ, JSON.stringify({ path: filePath }))
  }

  const handleMkdir = () => {
    const name = prompt('目录名称：')
    if (!name) return
    const dirPath = currentPath === '/' ? '/' + name : currentPath + '/' + name
    sendMsg(MSG_FILE_MKDIR_REQ, JSON.stringify({ path: dirPath }))
  }

  const formatSize = (bytes: number) => {
    if (bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'K', 'M', 'G']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return (bytes / Math.pow(k, i)).toFixed(i > 0 ? 1 : 0) + sizes[i]
  }

  // 拖拽上传
  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDraggingOver(true)
  }

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDraggingOver(false)
  }

  const handleDrop = async (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDraggingOver(false)
    if (uploading) return
    const file = e.dataTransfer.files?.[0]
    if (file) {
      await uploadFile(file)
    }
  }

  // 拖拽移动面板
  const handleDragStart = (e: React.MouseEvent) => {
    if (isFullscreen) return
    e.preventDefault()
    dragRef.current = { startX: e.clientX, startY: e.clientY, startPosX: pos.x, startPosY: pos.y }
    const onMove = (ev: MouseEvent) => {
      if (!dragRef.current) return
      setPos({
        x: dragRef.current.startPosX + ev.clientX - dragRef.current.startX,
        y: dragRef.current.startPosY + ev.clientY - dragRef.current.startY,
      })
    }
    const onUp = () => {
      dragRef.current = null
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }

  // 拖拽调整大小
  const handleResizeStart = (e: React.MouseEvent) => {
    if (isFullscreen) return
    e.preventDefault()
    e.stopPropagation()
    resizeRef.current = { startX: e.clientX, startY: e.clientY, startW: size.w, startH: size.h }
    const onMove = (ev: MouseEvent) => {
      if (!resizeRef.current) return
      setSize({
        w: Math.max(320, resizeRef.current.startW + ev.clientX - resizeRef.current.startX),
        h: Math.max(300, resizeRef.current.startH + ev.clientY - resizeRef.current.startY),
      })
    }
    const onUp = () => {
      resizeRef.current = null
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }

  const panelStyle: React.CSSProperties = isFullscreen
    ? { left: 0, top: 0, width: '100vw', height: '100vh', borderRadius: 0 }
    : { left: pos.x, top: pos.y, width: size.w, height: size.h, borderRadius: 8 }

  return (
    <div
      className="fixed z-[60] flex flex-col bg-gray-800 shadow-2xl border border-gray-600 overflow-hidden"
      style={panelStyle}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {/* 拖拽上传覆盖层 */}
      {isDraggingOver && (
        <div className="absolute inset-0 z-10 bg-blue-600/30 border-2 border-dashed border-blue-400 flex items-center justify-center pointer-events-none">
          <span className="text-blue-200 text-lg font-medium">拖放文件到此处上传</span>
        </div>
      )}

      {/* 标题栏 - 可拖拽 */}
      <div
        className="flex items-center justify-between px-3 py-2 bg-gray-900 border-b border-gray-700 shrink-0 select-none"
        onMouseDown={handleDragStart}
        style={{ cursor: isFullscreen ? 'default' : 'move' }}
      >
        <span className="text-xs font-semibold text-gray-300">
          {preview ? `预览: ${preview.name}` : 'SFTP 文件管理'}
        </span>
        <div className="flex items-center gap-1">
          <button
            onClick={(e) => { e.stopPropagation(); setIsFullscreen(v => !v) }}
            className="w-6 h-5 flex items-center justify-center rounded hover:bg-gray-700 text-gray-400 hover:text-white text-xs"
            title={isFullscreen ? '退出全屏' : '全屏'}
          >
            {isFullscreen ? '⊡' : '⊞'}
          </button>
          <button
            onClick={(e) => { e.stopPropagation(); onClose() }}
            className="w-6 h-5 flex items-center justify-center rounded hover:bg-red-700 text-gray-400 hover:text-white text-xs"
            title="关闭"
          >
            ✕
          </button>
        </div>
      </div>

      {/* 内容区域 */}
      {preview ? (
        /* 文本预览 */
        <div className="flex-1 flex flex-col min-h-0">
          <div className="flex items-center gap-1 px-2 py-1.5 border-b border-gray-700 shrink-0">
            <button onClick={() => setPreview(null)} className="px-2 py-0.5 rounded hover:bg-gray-700 text-xs text-gray-300">
              ← 返回列表
            </button>
            <span className="text-xs text-gray-500 truncate flex-1">{preview.name}</span>
            {preview.truncated && <span className="text-xs text-yellow-500">(已截断)</span>}
          </div>
          <pre className="flex-1 overflow-auto p-3 text-xs text-gray-200 whitespace-pre-wrap break-all font-mono leading-relaxed">
            {preview.content}
          </pre>
        </div>
      ) : (
        /* 文件列表 */
        <>
          {/* 路径栏 + 操作按钮 */}
          <div className="flex items-center gap-1 px-2 py-1.5 border-b border-gray-700 shrink-0">
            <input
              className="flex-1 min-w-0 bg-gray-700 border border-gray-600 rounded px-2 py-1 text-xs text-gray-200 focus:outline-none focus:border-blue-500"
              value={currentPath}
              onChange={e => setCurrentPath(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && navigateTo(currentPath)}
            />
            <button onClick={() => navigateTo(homePath)} className="px-1.5 py-1 rounded hover:bg-gray-600 text-xs" title="主目录">🏠</button>
            <button onClick={goUp} className="px-1.5 py-1 rounded hover:bg-gray-600 text-xs" title="上级目录">⬆</button>
            <button onClick={() => getFileList(currentPath)} className="px-1.5 py-1 rounded hover:bg-gray-600 text-xs" title="刷新">🔄</button>
            <label className={`px-1.5 py-1 rounded hover:bg-gray-600 text-xs cursor-pointer ${uploading ? 'opacity-50 pointer-events-none' : ''}`} title="上传文件">
              ⬆📁
              <input type="file" className="hidden" onChange={handleUpload} disabled={uploading} />
            </label>
            <button onClick={handleMkdir} className="px-1.5 py-1 rounded hover:bg-gray-600 text-xs" title="新建目录">📁+</button>
          </div>

          {/* 上传进度 */}
          {uploadProgress && (
            <div className="px-2 py-1 text-xs text-blue-300 border-b border-gray-700 shrink-0">
              {uploadProgress.name}: {formatSize(uploadProgress.loaded)} / {formatSize(uploadProgress.total)} ({Math.round(uploadProgress.loaded / uploadProgress.total * 100)}%)
              <div className="w-full bg-gray-700 rounded-full h-1 mt-1">
                <div className="bg-blue-500 h-1 rounded-full" style={{ width: `${(uploadProgress.loaded / uploadProgress.total) * 100}%` }} />
              </div>
            </div>
          )}

          {/* 错误提示 */}
          {error && (
            <div className="px-2 py-1 text-xs text-red-400 bg-red-900/20 border-b border-gray-700 shrink-0">
              {error}
            </div>
          )}

          {/* 文件列表 */}
          <div className="flex-1 overflow-y-auto">
            <table className="w-full">
              <thead className="sticky top-0 bg-gray-800 text-xs text-gray-400">
                <tr>
                  <th className="text-left px-2 py-1 font-normal">名称</th>
                  <th className="text-right px-2 py-1 font-normal w-16">大小</th>
                  <th className="text-right px-2 py-1 font-normal w-28">修改时间</th>
                  <th className="text-center px-1 py-1 font-normal w-8"></th>
                </tr>
              </thead>
              <tbody>
                {files.map(f => (
                  <tr
                    key={f.name}
                    className="hover:bg-gray-700/50 cursor-pointer border-t border-gray-800"
                    onClick={() => handleRowClick(f)}
                  >
                    <td className="px-2 py-0.5 truncate max-w-[200px]">
                      <span className={f.is_dir ? 'text-blue-400' : 'text-gray-300'}>
                        {f.is_dir ? '📁 ' : '📄 '}{f.name}
                      </span>
                    </td>
                    <td className="px-2 py-0.5 text-right text-gray-400 text-xs">
                      {f.is_dir ? '-' : formatSize(f.size)}
                    </td>
                    <td className="px-2 py-0.5 text-right text-gray-500 text-xs">
                      {f.mod_time?.slice(5, 16) || '-'}
                    </td>
                    <td className="px-1 py-0.5 text-center" onClick={e => e.stopPropagation()}>
                      {!f.is_dir && (
                        <button
                          onClick={() => handleDelete(f)}
                          className="text-gray-600 hover:text-red-400 text-xs"
                          title="删除"
                        >✕</button>
                      )}
                    </td>
                  </tr>
                ))}
                {files.length === 0 && !error && (
                  <tr><td colSpan={4} className="text-center py-4 text-gray-500 text-xs">空目录</td></tr>
                )}
              </tbody>
            </table>
          </div>

          {/* 底部提示 */}
          <div className="px-2 py-1 text-[10px] text-gray-500 border-t border-gray-700 shrink-0">
            拖放文件到面板上传 · 文本文件点击预览 · 其他文件点击下载
          </div>
        </>
      )}

      {/* 右下角调整大小手柄 */}
      {!isFullscreen && (
        <div
          className="absolute right-0 bottom-0 w-4 h-4 cursor-se-resize"
          onMouseDown={handleResizeStart}
        >
          <svg className="w-4 h-4 text-gray-600" viewBox="0 0 16 16">
            <path d="M14 14L14 8M14 14L8 14M10 14L14 10M14 14L12 14" stroke="currentColor" strokeWidth="1.5" fill="none" />
          </svg>
        </div>
      )}

      {/* 预览加载中 */}
      {previewLoading && (
        <div className="absolute inset-0 bg-gray-900/60 flex items-center justify-center z-20">
          <span className="text-gray-300 text-sm">加载中...</span>
        </div>
      )}
    </div>
  )
}
