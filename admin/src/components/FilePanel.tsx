import { useState, useRef, useCallback, useEffect } from 'react'
import { marked } from 'marked'
import { markedHighlight } from 'marked-highlight'
import hljs from 'highlight.js/lib/core'
import DOMPurify from 'dompurify'

// 按需加载 highlight.js 语言（控制包体积）
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import python from 'highlight.js/lib/languages/python'
import go from 'highlight.js/lib/languages/go'
import rust from 'highlight.js/lib/languages/rust'
import java from 'highlight.js/lib/languages/java'
import cpp from 'highlight.js/lib/languages/cpp'
import c from 'highlight.js/lib/languages/c'
import csharp from 'highlight.js/lib/languages/csharp'
import bash from 'highlight.js/lib/languages/bash'
import sql from 'highlight.js/lib/languages/sql'
import json from 'highlight.js/lib/languages/json'
import yaml from 'highlight.js/lib/languages/yaml'
import xml from 'highlight.js/lib/languages/xml'
import css from 'highlight.js/lib/languages/css'
import markdown from 'highlight.js/lib/languages/markdown'
import ini from 'highlight.js/lib/languages/ini'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import makefile from 'highlight.js/lib/languages/makefile'
import lua from 'highlight.js/lib/languages/lua'
import php from 'highlight.js/lib/languages/php'
import ruby from 'highlight.js/lib/languages/ruby'
import shell from 'highlight.js/lib/languages/shell'
import plaintext from 'highlight.js/lib/languages/plaintext'

hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('python', python)
hljs.registerLanguage('go', go)
hljs.registerLanguage('rust', rust)
hljs.registerLanguage('java', java)
hljs.registerLanguage('cpp', cpp)
hljs.registerLanguage('c', c)
hljs.registerLanguage('csharp', csharp)
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('shell', shell)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('json', json)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('css', css)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('ini', ini)
hljs.registerLanguage('dockerfile', dockerfile)
hljs.registerLanguage('makefile', makefile)
hljs.registerLanguage('lua', lua)
hljs.registerLanguage('php', php)
hljs.registerLanguage('ruby', ruby)
hljs.registerLanguage('plaintext', plaintext)

// 配置 marked + highlight.js
marked.use(markedHighlight({
  langPrefix: 'hljs language-',
  highlight(code: string, lang: string) {
    if (lang && hljs.getLanguage(lang)) {
      return hljs.highlight(code, { language: lang }).value
    }
    return hljs.highlightAuto(code).value
  },
}))
marked.use({ breaks: true, gfm: true })

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

const MARKDOWN_EXTS = new Set(['md', 'markdown', 'mdx'])

const CODE_EXTENSIONS = new Set([
  'js', 'jsx', 'ts', 'tsx', 'mjs', 'cjs',
  'py', 'rb', 'go', 'rs', 'java', 'c', 'h', 'cpp', 'hpp', 'cc', 'cxx',
  'cs', 'swift', 'kt', 'scala', 'ex', 'exs', 'erl', 'hs', 'ml', 'r', 'lua',
  'php', 'perl', 'pl',
  'sh', 'bash', 'zsh', 'fish', 'ps1', 'bat', 'cmd',
  'html', 'htm', 'css', 'scss', 'sass', 'less', 'vue', 'svelte', 'astro',
  'json', 'yaml', 'yml', 'toml', 'xml', 'svg',
  'sql', 'graphql', 'gql',
  'tf', 'hcl', 'nomad',
  'dockerfile', 'makefile', 'cmake', 'gradle',
  'ini', 'conf', 'cfg', 'properties', 'env', 'service', 'socket', 'timer',
  'gitignore', 'dockerignore', 'editorconfig',
])

const PLAIN_TEXT_EXTENSIONS = new Set([
  'txt', 'log', 'csv', 'tsv', 'readme', 'license', 'changelog',
])

type PreviewMode = 'markdown' | 'code' | 'text' | null

function getPreviewMode(name: string): PreviewMode {
  const lower = name.toLowerCase()
  // 无扩展名的特殊文件
  if (['makefile', 'dockerfile', 'vagrantfile', 'gemfile', 'rakefile', 'procfile'].some(f => lower === f)) return 'code'
  if (['license', 'readme', 'changelog'].some(f => lower === f)) return 'markdown'
  const dot = lower.lastIndexOf('.')
  if (dot < 0) return null
  const ext = lower.slice(dot + 1)
  if (MARKDOWN_EXTS.has(ext)) return 'markdown'
  if (CODE_EXTENSIONS.has(ext)) return 'code'
  if (PLAIN_TEXT_EXTENSIONS.has(ext)) return 'text'
  return null
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
  const [preview, setPreview] = useState<{ name: string; content: string; truncated: boolean; mode: PreviewMode } | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [theme, setTheme] = useState<'dark' | 'light'>('dark')

  // 面板位置和大小
  const [pos, setPos] = useState({ x: -1, y: -1 }) // -1 表示未初始化
  const [size, setSize] = useState({ w: 420, h: 500 })
  const dragRef = useRef<{ startX: number; startY: number; startPosX: number; startPosY: number } | null>(null)
  const resizeRef = useRef<{ startX: number; startY: number; startW: number; startH: number } | null>(null)

  const downloadChunks = useRef<Uint8Array[]>([])
  const downloading = useRef(false)
  const readingRef = useRef<{ name: string; mode: PreviewMode } | null>(null)
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
        case MSG_FILE_ACK_RESP: {
          const ack = JSON.parse(text())
          setPreviewLoading(false)
          if (ack.ok && downloading.current && downloadChunks.current.length > 0) {
            if (readingRef.current) {
              // 文件预览：拼接 chunks 为文本
              const content = downloadChunks.current.map(c => new TextDecoder().decode(c)).join('')
              const { name, mode } = readingRef.current
              setPreview({ name, content, truncated: false, mode })
              readingRef.current = null
            } else {
              // 文件下载：组装 Blob
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
            }
            downloadChunks.current = []
            downloading.current = false
          } else if (!ack.ok) {
            setError(ack.msg || 'operation failed')
            downloadChunks.current = []
            downloading.current = false
            readingRef.current = null
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
    } else if (getPreviewMode(file.name)) {
      // 可预览文件 → 预览
      const filePath = currentPath === '/' ? '/' + file.name : currentPath + '/' + file.name
      readingRef.current = { name: file.name, mode: getPreviewMode(file.name) || 'text' }
      downloadChunks.current = []
      downloading.current = true
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
      className={`fixed z-[60] flex flex-col shadow-2xl border overflow-hidden theme-${theme} ${theme === 'dark' ? 'bg-gray-800 border-gray-600' : 'bg-white border-gray-300'}`}
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
        className={`flex items-center justify-between px-3 py-2 border-b shrink-0 select-none ${theme === 'dark' ? 'bg-gray-900 border-gray-700' : 'bg-gray-50 border-gray-300'}`}
        onMouseDown={handleDragStart}
        style={{ cursor: isFullscreen ? 'default' : 'move' }}
      >
        <span className={`text-xs font-semibold ${theme === 'dark' ? 'text-gray-300' : 'text-gray-700'}`}>
          {preview ? `预览: ${preview.name}` : 'SFTP 文件管理'}
        </span>
        <div className="flex items-center gap-1">
          <button
            onClick={(e) => { e.stopPropagation(); setTheme(t => t === 'dark' ? 'light' : 'dark') }}
            className={`w-6 h-5 flex items-center justify-center rounded text-xs ${theme === 'dark' ? 'hover:bg-gray-700 text-gray-400 hover:text-white' : 'hover:bg-gray-200 text-gray-500 hover:text-gray-800'}`}
            title={theme === 'dark' ? '切换亮色主题' : '切换暗色主题'}
          >
            {theme === 'dark' ? '☀' : '☾'}
          </button>
          <button
            onClick={(e) => { e.stopPropagation(); setIsFullscreen(v => !v) }}
            className={`w-6 h-5 flex items-center justify-center rounded text-xs ${theme === 'dark' ? 'hover:bg-gray-700 text-gray-400 hover:text-white' : 'hover:bg-gray-200 text-gray-500 hover:text-gray-800'}`}
            title={isFullscreen ? '退出全屏' : '全屏'}
          >
            {isFullscreen ? '⊡' : '⊞'}
          </button>
          <button
            onClick={(e) => { e.stopPropagation(); onClose() }}
            className={`w-6 h-5 flex items-center justify-center rounded text-xs ${theme === 'dark' ? 'hover:bg-red-700 text-gray-400 hover:text-white' : 'hover:bg-red-100 text-gray-500 hover:text-red-600'}`}
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
          <div className={`flex items-center gap-1 px-2 py-1.5 border-b shrink-0 ${theme === 'dark' ? 'border-gray-700' : 'border-gray-200'}`}>
            <button onClick={() => setPreview(null)} className={`px-2 py-0.5 rounded text-xs ${theme === 'dark' ? 'hover:bg-gray-700 text-gray-300' : 'hover:bg-gray-200 text-gray-600'}`}>
              ← 返回列表
            </button>
            <span className={`text-xs truncate flex-1 ${theme === 'dark' ? 'text-gray-500' : 'text-gray-400'}`}>{preview.name}</span>
            {preview.truncated && <span className="text-xs text-yellow-500">(已截断)</span>}
          </div>
          {preview.mode === 'markdown' ? (
            <div
              className={`flex-1 overflow-auto p-5 max-w-none file-preview-prose ${theme === 'dark' ? 'prose prose-invert' : 'prose'}`}
              style={{ fontSize: '14px', lineHeight: '1.8', background: 'var(--fp-bg)', color: 'var(--fp-text)' }}
              dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(marked.parse(preview.content) as string) }}
            />
          ) : preview.mode === 'code' ? (
            <pre className="flex-1 overflow-auto p-4" style={{ background: 'var(--fp-bg)', margin: 0 }}>
              <code
                className={`hljs hljs-${theme}`}
                style={{ fontSize: '13px', lineHeight: '1.7', fontFamily: 'Menlo, Monaco, Consolas, "Courier New", monospace' }}
                dangerouslySetInnerHTML={{
                  __html: (() => {
                    const ext = preview.name.split('.').pop()?.toLowerCase()
                    const langMap: Record<string, string> = {
                      js: 'javascript', mjs: 'javascript', cjs: 'javascript',
                      ts: 'typescript', tsx: 'typescript', jsx: 'javascript',
                      py: 'python', rb: 'ruby', rs: 'rust', kt: 'kotlin',
                      sh: 'bash', zsh: 'bash', fish: 'shell',
                      yml: 'yaml', md: 'markdown', htm: 'html',
                      h: 'c', hpp: 'cpp', cc: 'cpp', cxx: 'cpp',
                    }
                    const lang = ext && (langMap[ext] || (hljs.getLanguage(ext) ? ext : null))
                    return lang
                      ? hljs.highlight(preview.content, { language: lang }).value
                      : hljs.highlightAuto(preview.content).value
                  })()
                }}
              />
            </pre>
          ) : (
            <pre className={`flex-1 overflow-auto p-4 font-mono ${theme === 'dark' ? 'text-gray-200' : 'text-gray-800'}`} style={{ fontSize: '13px', lineHeight: '1.7', whiteSpace: 'pre-wrap', wordBreak: 'break-word', background: 'var(--fp-bg)' }}>
              {preview.content}
            </pre>
          )}
        </div>
      ) : (
        /* 文件列表 */
        <>
          {/* 路径栏 + 操作按钮 */}
          <div className={`flex items-center gap-1 px-2 py-1.5 border-b shrink-0 ${theme === 'dark' ? 'border-gray-700' : 'border-gray-200'}`}>
            <button onClick={goUp} className={`px-1.5 py-1 rounded text-xs ${theme === 'dark' ? 'hover:bg-gray-600' : 'hover:bg-gray-200'}`} title="上级目录">⬆</button>
            <input
              className={`flex-1 min-w-0 rounded px-2 py-1 text-xs focus:outline-none focus:border-blue-500 ${theme === 'dark' ? 'bg-gray-700 border border-gray-600 text-gray-200' : 'bg-gray-100 border border-gray-300 text-gray-800'}`}
              value={currentPath}
              onChange={e => setCurrentPath(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && navigateTo(currentPath)}
            />
            <button onClick={() => navigateTo(homePath)} className={`px-1.5 py-1 rounded text-xs ${theme === 'dark' ? 'hover:bg-gray-600' : 'hover:bg-gray-200'}`} title="主目录">🏠</button>
            <button onClick={() => getFileList(currentPath)} className={`px-1.5 py-1 rounded text-xs ${theme === 'dark' ? 'hover:bg-gray-600' : 'hover:bg-gray-200'}`} title="刷新">🔄</button>
            <label className={`px-1.5 py-1 rounded text-xs cursor-pointer ${theme === 'dark' ? 'hover:bg-gray-600' : 'hover:bg-gray-200'} ${uploading ? 'opacity-50 pointer-events-none' : ''}`} title="上传文件">
              ⬆📁
              <input type="file" className="hidden" onChange={handleUpload} disabled={uploading} />
            </label>
            <button onClick={handleMkdir} className={`px-1.5 py-1 rounded text-xs ${theme === 'dark' ? 'hover:bg-gray-600' : 'hover:bg-gray-200'}`} title="新建目录">📁+</button>
          </div>

          {/* 上传进度 */}
          {uploadProgress && (
            <div className={`px-2 py-1 text-xs text-blue-300 border-b shrink-0 ${theme === 'dark' ? 'border-gray-700' : 'border-gray-200'}`}>
              {uploadProgress.name}: {formatSize(uploadProgress.loaded)} / {formatSize(uploadProgress.total)} ({Math.round(uploadProgress.loaded / uploadProgress.total * 100)}%)
              <div className={`w-full rounded-full h-1 mt-1 ${theme === 'dark' ? 'bg-gray-700' : 'bg-gray-200'}`}>
                <div className="bg-blue-500 h-1 rounded-full" style={{ width: `${(uploadProgress.loaded / uploadProgress.total) * 100}%` }} />
              </div>
            </div>
          )}

          {/* 错误提示 */}
          {error && (
            <div className={`px-2 py-1 text-xs text-red-400 border-b shrink-0 ${theme === 'dark' ? 'bg-red-900/20 border-gray-700' : 'bg-red-50 border-gray-200'}`}>
              {error}
            </div>
          )}

          {/* 文件列表 */}
          <div className="flex-1 overflow-y-auto">
            <table className="w-full">
              <thead className={`sticky top-0 text-xs ${theme === 'dark' ? 'bg-gray-800 text-gray-400' : 'bg-white text-gray-500'}`}>
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
                    className={`cursor-pointer ${theme === 'dark' ? 'hover:bg-gray-700/50 border-t border-gray-800' : 'hover:bg-gray-100 border-t border-gray-200'}`}
                    onClick={() => handleRowClick(f)}
                  >
                    <td className="px-2 py-0.5 truncate max-w-[200px]">
                      <span className={f.is_dir ? 'text-blue-400' : theme === 'dark' ? 'text-gray-300' : 'text-gray-700'}>
                        {f.is_dir ? '📁 ' : '📄 '}{f.name}
                      </span>
                    </td>
                    <td className={`px-2 py-0.5 text-right text-xs ${theme === 'dark' ? 'text-gray-400' : 'text-gray-500'}`}>
                      {f.is_dir ? '-' : formatSize(f.size)}
                    </td>
                    <td className={`px-2 py-0.5 text-right text-xs ${theme === 'dark' ? 'text-gray-500' : 'text-gray-400'}`}>
                      {f.mod_time?.slice(5, 16) || '-'}
                    </td>
                    <td className="px-1 py-0.5 text-center" onClick={e => e.stopPropagation()}>
                      {!f.is_dir && (
                        <button
                          onClick={() => handleDelete(f)}
                          className={`${theme === 'dark' ? 'text-gray-600 hover:text-red-400' : 'text-gray-400 hover:text-red-500'} text-xs`}
                          title="删除"
                        >✕</button>
                      )}
                    </td>
                  </tr>
                ))}
                {files.length === 0 && !error && (
                  <tr><td colSpan={4} className={`text-center py-4 text-xs ${theme === 'dark' ? 'text-gray-500' : 'text-gray-400'}`}>空目录</td></tr>
                )}
              </tbody>
            </table>
          </div>

          {/* 底部提示 */}
          <div className={`px-2 py-1 text-[10px] border-t shrink-0 ${theme === 'dark' ? 'text-gray-500 border-gray-700' : 'text-gray-400 border-gray-200'}`}>
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
        <div className={`absolute inset-0 flex items-center justify-center z-20 ${theme === 'dark' ? 'bg-gray-900/60' : 'bg-white/60'}`}>
          <span className={`text-sm ${theme === 'dark' ? 'text-gray-300' : 'text-gray-600'}`}>加载中...</span>
        </div>
      )}
    </div>
  )
}
