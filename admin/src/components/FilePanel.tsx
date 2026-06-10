import { useState, useRef, useCallback, useEffect } from 'react'

// 消息类型常量
const MSG_FILE_LIST_REQ = 0x04
const MSG_FILE_UPLOAD_REQ = 0x05
const MSG_FILE_UPLOAD_DATA = 0x06
const MSG_FILE_UPLOAD_END = 0x07
const MSG_FILE_DOWNLOAD_REQ = 0x08
const MSG_FILE_DELETE_REQ = 0x09
const MSG_FILE_MKDIR_REQ = 0x0A

const MSG_FILE_LIST_RESP = 0x0C
const MSG_FILE_DATA_RESP = 0x0D
const MSG_FILE_ACK_RESP = 0x0E

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
}

export function FilePanel({ ws, onRegister }: FilePanelProps) {
  const [files, setFiles] = useState<FileEntry[]>([])
  const [currentPath, setCurrentPath] = useState('/')
  const [homePath, setHomePath] = useState('/')
  const [error, setError] = useState('')
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<UploadProgress | null>(null)
  const downloadChunks = useRef<Uint8Array[]>([])
  const downloading = useRef(false)
  const uploadingRef = useRef(false)

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
    } else if (!downloading.current) {
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

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const fileList = e.target.files
    if (!fileList || fileList.length === 0) return
    const file = fileList[0]
    const filePath = currentPath === '/' ? '/' + file.name : currentPath + '/' + file.name

    setUploading(true)
    uploadingRef.current = true
    setUploadProgress({ loaded: 0, total: file.size, name: file.name })

    sendMsg(MSG_FILE_UPLOAD_REQ, JSON.stringify({ path: filePath, size: file.size }))

    const CHUNK = 32768
    const BUFFER_THRESHOLD = CHUNK * 4 // 128KB 缓冲上限
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

  return (
    <div className="flex flex-col h-full text-sm text-gray-200">
      {/* 标题 */}
      <div className="px-3 py-2 text-xs font-semibold text-gray-400 border-b border-gray-700">
        SFTP 文件管理
      </div>

      {/* 路径栏 + 操作按钮 */}
      <div className="flex items-center gap-1 px-2 py-1.5 border-b border-gray-700 shrink-0">
        <input
          className="flex-1 bg-gray-700 border border-gray-600 rounded px-2 py-1 text-xs text-gray-200 focus:outline-none focus:border-blue-500"
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
        <div className="px-2 py-1 bg-gray-750 text-xs text-blue-300 border-b border-gray-700 shrink-0">
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
                <td className="px-2 py-0.5 truncate max-w-[120px]">
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
    </div>
  )
}
