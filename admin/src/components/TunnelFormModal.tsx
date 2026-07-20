import { useMemo, useState } from 'react'
import { api } from '../api/client'
import type { Node, Tunnel, TunnelPara, TunnelRateLimit, SerialConfig, BinaryConfig, VNTConfig, LifecycleConfig, WatchdogConfig, LogConfig } from '../types/api'
import { Modal } from './Modal'
import { FormField } from './FormField'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'
import { formatBandwidth } from '../lib/utils'

interface TunnelFormModalProps {
  tunnel?: Tunnel
  presetNodeId?: string
  defaultType?: TunnelType
  onClose: () => void
  onSuccess: () => void
}

type TunnelType = 'http' | 'https' | 'tcp' | 'udp' | 'ser2mq' | 'vpn-manager' | 'ser2tcp' | 'ser2udp' | 'webssh'

const BAUDRATE_OPTIONS = [1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200]
const DATABITS_OPTIONS = [5, 6, 7, 8]
const STOPBITS_OPTIONS = ['1', '1.5', '2']
const PARITY_OPTIONS = [
  { value: 'N', label: '无 (N)' },
  { value: 'E', label: '偶 (E)' },
  { value: 'O', label: '奇 (O)' },
]

const tunnelTypeRow1: { value: TunnelType; label: string }[] = [
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' },
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
  { value: 'webssh', label: 'WebSSH' },
]

const tunnelTypeRow2: { value: TunnelType; label: string }[] = [
  { value: 'ser2mq', label: 'Ser2MQ' },
  { value: 'vpn-manager', label: 'VPN' },
  { value: 'ser2tcp', label: 'Ser2TCP' },
  { value: 'ser2udp', label: 'Ser2UDP' },
]

export function TunnelFormModal({ tunnel, presetNodeId, defaultType, onClose, onSuccess }: TunnelFormModalProps) {
  const { toast } = useToast()
  const isEdit = !!tunnel

  const [name, setName] = useState(tunnel?.name || '')
  const [type, setType] = useState<TunnelType>(tunnel?.type || defaultType || 'http')
  const [target, setTarget] = useState(tunnel?.target || '')
  const [domain, setDomain] = useState(tunnel?.domain || '')
  const [listenPort, setListenPort] = useState(tunnel?.listen_port?.toString() || '')
  const [nodeId, setNodeId] = useState(presetNodeId || tunnel?.node_id || '')
  const originalNodeId = isEdit ? (tunnel?.node_id || '') : ''
  const [enabled, setEnabled] = useState(tunnel?.enabled !== false)

  // 限速配置 — 根据 bytes/sec 自动选择单位
  const initBw = tunnel?.rate_limit?.max_bandwidth || 0
  const initUnit: 'bps' | 'kbps' | 'mbps' = initBw >= 1024 * 1024 ? 'mbps' : initBw >= 1024 ? 'kbps' : 'bps'
  const initBwDisplay = initBw >= 1024 * 1024 ? (initBw / 1024 / 1024).toFixed(1) : initBw >= 1024 ? String(Math.round(initBw / 1024)) : initBw > 0 ? String(initBw) : ''
  const [rlMaxConns, setRlMaxConns] = useState(tunnel?.rate_limit?.max_conns?.toString() || '')
  const [rlMaxBandwidth, setRlMaxBandwidth] = useState(initBwDisplay)
  const [rlBwUnit, setRlBwUnit] = useState<'bps' | 'kbps' | 'mbps'>(initUnit)
  const isStandardType = ['http', 'https', 'tcp', 'udp'].includes(type)

  // ser2mq 配置
  const [ser2mqBroker, setSer2mqBroker] = useState(tunnel?.para?.broker || '')
  const [ser2mqSerialPort, setSer2mqSerialPort] = useState(tunnel?.para?.serial?.port || '/dev/ttyS0')
  const [ser2mqBaudrate, setSer2mqBaudrate] = useState(tunnel?.para?.serial?.baudrate?.toString() || '9600')
  const [ser2mqDatabits, setSer2mqDatabits] = useState(tunnel?.para?.serial?.databits?.toString() || '8')
  const [ser2mqStopbits, setSer2mqStopbits] = useState(tunnel?.para?.serial?.stopbits?.toString() || '1')
  const [ser2mqParity, setSer2mqParity] = useState(tunnel?.para?.serial?.parity || 'N')
  const [ser2mqTimeout, setSer2mqTimeout] = useState(tunnel?.para?.serial?.timeout?.toString() || '3000')
  const [ser2mqSecret, setSer2mqSecret] = useState(tunnel?.para?.secret || '')
  const [ser2mqQoS, setSer2mqQoS] = useState(tunnel?.para?.qos?.toString() || '1')

  // vpn-manager 配置
  const [vpnBinaryName, setVpnBinaryName] = useState(tunnel?.para?.binary?.name || '')
  const [vpnBinaryPath, setVpnBinaryPath] = useState(tunnel?.para?.binary?.path || '')
  const [vpnArgs, setVpnArgs] = useState(tunnel?.para?.args?.join(' ') || '')
  // vnt-cli 专用配置
  const [vntEnabled, setVntEnabled] = useState(tunnel?.para?.vnt?.enabled ?? true)
  const [vntToken, setVntToken] = useState(tunnel?.para?.vnt?.token || '')
  const [vntServer, setVntServer] = useState(tunnel?.para?.vnt?.server || '')
  const [vntDeviceID, setVntDeviceID] = useState(tunnel?.para?.vnt?.device_id || '')
  const [vntDeviceName, setVntDeviceName] = useState(tunnel?.para?.vnt?.name || '')
  const [vntPassword, setVntPassword] = useState(tunnel?.para?.vnt?.password || '')
  const [vntInIP, setVntInIP] = useState(tunnel?.para?.vnt?.in_ip || '')
  const [vntOutIP, setVntOutIP] = useState(tunnel?.para?.vnt?.out_ip || '')
  const [vntVirtualIP, setVntVirtualIP] = useState(tunnel?.para?.vnt?.ip || '')
  const [vntRestPort, setVntRestPort] = useState(tunnel?.para?.vnt?.rest_port?.toString() || '59871')
  const [vpnAutostart, setVpnAutostart] = useState(tunnel?.para?.lifecycle?.autostart || false)
  const [vpnRestartOnCrash, setVpnRestartOnCrash] = useState(tunnel?.para?.lifecycle?.restart_on_crash ?? true)
  const [vpnMaxRestarts, setVpnMaxRestarts] = useState(tunnel?.para?.lifecycle?.max_restarts?.toString() || '3')
  const [vpnRestartDelay, setVpnRestartDelay] = useState(tunnel?.para?.lifecycle?.restart_delay?.toString() || '5')
  const [vpnLogCapture, setVpnLogCapture] = useState(tunnel?.para?.log?.capture ?? true)
  const [vpnLogMaxSize, setVpnLogMaxSize] = useState(tunnel?.para?.log?.max_size?.toString() || '65536')
  const [vpnLogPath, setVpnLogPath] = useState(tunnel?.para?.log?.output_path || '')

  // ser2net 配置 (ser2tcp/ser2udp)
  const [snMode, setSnMode] = useState(tunnel?.para?.mode || 'server')
  const [snAddress, setSnAddress] = useState(tunnel?.para?.address || ':5000')
  const [snMaxConn, setSnMaxConn] = useState(tunnel?.para?.max_conn?.toString() || '1')
  const [snSerialPort, setSnSerialPort] = useState(tunnel?.para?.serial?.port || '/dev/ttyS0')
  const [snBaudrate, setSnBaudrate] = useState(tunnel?.para?.serial?.baudrate?.toString() || '9600')
  const [snDatabits, setSnDatabits] = useState(tunnel?.para?.serial?.databits?.toString() || '8')
  const [snStopbits, setSnStopbits] = useState(tunnel?.para?.serial?.stopbits?.toString() || '1')
  const [snParity, setSnParity] = useState(tunnel?.para?.serial?.parity || 'N')
  const [snTimeout, setSnTimeout] = useState(tunnel?.para?.serial?.timeout?.toString() || '3000')

  // webssh 配置
  const [sshHost, setSshHost] = useState(tunnel?.para?.host || '')
  const [sshPort, setSshPort] = useState(tunnel?.para?.port?.toString() || '22')
  const [sshUser, setSshUser] = useState(tunnel?.para?.user || 'root')
  const [sshAuthType, setSshAuthType] = useState<'password' | 'key'>(tunnel?.para?.auth_type || 'password')
  const [sshPassword, setSshPassword] = useState(tunnel?.para?.password || '')
  const [sshPrivKey, setSshPrivKey] = useState(tunnel?.para?.priv_key || '')

  const [submitting, setSubmitting] = useState(false)

  const { data: nodesResponse, loading: nodesLoading } = useRequest(
    () => api.getNodes(),
    { onError: () => {} },
  )
  const onlineNodes = useMemo(
    () => (nodesResponse?.items || []).filter((node: Node) => node.status === 'online'),
    [nodesResponse],
  )
  const allNodes = useMemo(
    () => (nodesResponse?.items || []),
    [nodesResponse],
  )

  const buildPara = (): TunnelPara | undefined => {
    if (type === 'ser2mq') {
      return {
        enable: enabled,
        broker: ser2mqBroker,
        serial: {
          port: ser2mqSerialPort,
          baudrate: Number(ser2mqBaudrate),
          databits: Number(ser2mqDatabits),
          stopbits: Number(ser2mqStopbits),
          parity: ser2mqParity,
          timeout: Number(ser2mqTimeout) || 3000,
        },
        secret: ser2mqSecret,
        qos: Number(ser2mqQoS) || 1,
      }
    }
    if (type === 'vpn-manager') {
      const vnt: VNTConfig = {
        enabled: vntEnabled,
        token: vntToken,
        server: vntServer,
        device_id: vntDeviceID,
        name: vntDeviceName,
        password: vntPassword,
        in_ip: vntInIP,
        out_ip: vntOutIP,
        ip: vntVirtualIP,
        rest_port: Number(vntRestPort) || 59871,
      }
      return {
        binary: {
          name: vpnBinaryName,
          path: vpnBinaryPath,
        },
        args: vpnArgs.split(' ').filter(s => s.trim()),
        vnt: vntEnabled ? vnt : undefined,
        lifecycle: {
          autostart: vpnAutostart,
          restart_on_crash: vpnRestartOnCrash,
          max_restarts: Number(vpnMaxRestarts),
          restart_delay: Number(vpnRestartDelay),
        },
        watchdog: {
          enabled: true,
          interval: 10,
          quit_grace: 10,
        },
        log: {
          capture: vpnLogCapture,
          max_size: Number(vpnLogMaxSize),
          output_path: vpnLogPath,
        },
      }
    }
    if (type === 'ser2tcp' || type === 'ser2udp') {
      return {
        enable: enabled,
        mode: snMode,
        address: snAddress,
        max_conn: Number(snMaxConn) || 1,
        serial: {
          port: snSerialPort,
          baudrate: Number(snBaudrate),
          databits: Number(snDatabits),
          stopbits: Number(snStopbits),
          parity: snParity,
          timeout: Number(snTimeout) || 3000,
        },
      }
    }
    if (type === 'webssh') {
      return {
        enable: enabled,
        host: sshHost,
        port: Number(sshPort) || 22,
        user: sshUser,
        auth_type: sshAuthType,
        // 两字段都发：切换 auth_type 时保留各自凭证，避免反复重填。
        // 空值（undefined）不下发；编辑回显的密文 enc: 由后端幂等处理。
        password: sshPassword || undefined,
        priv_key: sshPrivKey || undefined,
      }
    }
    return undefined
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    if (!name.trim()) {
      toast('请输入隧道名称', 'error')
      return
    }
    if (type === 'http' || type === 'https') {
      if (!target.trim()) {
        toast('请输入目标地址', 'error')
        return
      }
    }
    if (type === 'tcp' || type === 'udp') {
      if (!listenPort.trim()) {
        toast('请输入监听端口', 'error')
        return
      }
    }
    if (type === 'ser2mq') {
      if (!ser2mqBroker.trim()) {
        toast('请输入 MQTT Broker 地址', 'error')
        return
      }
      if (!ser2mqSecret.trim()) {
        toast('请输入加密密钥', 'error')
        return
      }
      if (ser2mqSecret.length !== 64) {
        toast('加密密钥必须为 64 字符（32 字节 hex）', 'error')
        return
      }
    }
    if (type === 'vpn-manager') {
      if (!vpnBinaryName.trim()) {
        toast('请输入程序名称', 'error')
        return
      }
    }
    if (type === 'ser2tcp' || type === 'ser2udp') {
      if (!snSerialPort.trim()) {
        toast('请输入串口端口', 'error')
        return
      }
      if (!snAddress.trim()) {
        toast('请输入地址', 'error')
        return
      }
    }
    if (type === 'webssh') {
      if (!sshHost.trim()) {
        toast('请输入 SSH 主机地址', 'error')
        return
      }
      if (!sshUser.trim()) {
        toast('请输入用户名', 'error')
        return
      }
      if (sshAuthType === 'password' && !sshPassword) {
        toast('请输入密码', 'error')
        return
      }
      if (sshAuthType === 'key' && !sshPrivKey.trim()) {
        toast('请输入私钥', 'error')
        return
      }
    }
    if (!isEdit && !nodeId) {
      toast('请选择节点', 'error')
      return
    }
    if (isEdit && !nodeId) {
      toast('请选择节点', 'error')
      return
    }

    try {
      setSubmitting(true)
      // 构建 payload，支持所有隧道类型
      const buildRateLimit = (): TunnelRateLimit | null | undefined => {
        if (!isStandardType) return undefined
        const conns = Number(rlMaxConns)
        let bw = Number(rlMaxBandwidth)
        if (rlBwUnit === 'kbps') bw *= 1024
        else if (rlBwUnit === 'mbps') bw *= 1024 * 1024
        if (conns > 0 || bw > 0) return { max_conns: conns || 0, max_bandwidth: bw || 0 }
        if (isEdit && !rlMaxConns && !rlMaxBandwidth) return null
        return undefined
      }
      const payload = {
        name: name.trim(),
        type,
        enabled,
        node_id: nodeId,
        ...(isEdit && originalNodeId && originalNodeId !== nodeId ? { original_node_id: originalNodeId } : {}),
        target: (() => {
          switch (type) {
            case 'http': case 'https': return target.trim()
            case 'tcp': case 'udp': return target.trim() || '127.0.0.1:0'
            case 'ser2mq': return ser2mqSerialPort
            case 'vpn-manager': return vpnBinaryName
            case 'ser2tcp': case 'ser2udp': return snSerialPort
            case 'webssh': return `${sshHost}:${sshPort}`
            default: return target.trim()
          }
        })(),
        domain: (type === 'http' || type === 'https') && domain.trim() ? domain.trim() : undefined,
        listen_port: (type === 'tcp' || type === 'udp') ? Number(listenPort) : undefined,
        para: (type === 'ser2mq' || type === 'vpn-manager' || type === 'ser2tcp' || type === 'ser2udp' || type === 'webssh') ? buildPara() : undefined,
        rate_limit: buildRateLimit(),
      }

      if (isEdit) {
        await api.updateTunnel(payload)
      } else {
        await api.createTunnel(payload)
      }
      toast(isEdit ? '隧道已更新' : '隧道已创建', 'success')
      onSuccess()
    } catch (err: unknown) {
      toast((err as Error).message || '操作失败', 'error')
    } finally {
      setSubmitting(false)
    }
  }

  const inputClass = 'w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary'
  const selectClass = inputClass

  return (
    <Modal title={isEdit ? '编辑隧道' : '创建隧道'} onClose={onClose} size="lg">
      <form onSubmit={handleSubmit} className="space-y-4">
        <FormField label="隧道名称">
          <input
            type="text"
            value={name}
            onChange={e => setName(e.target.value)}
            disabled={isEdit}
            placeholder="例如: my-web"
            className={inputClass}
          />
          {isEdit && (
            <p className="mt-1 text-xs text-gray-500">当前后端以同节点下的隧道名称作为更新定位标识，编辑时不可修改名称。</p>
          )}
        </FormField>

        <FormField label="类型">
          <div className="space-y-2">
            <div className="flex flex-wrap gap-2">
              {tunnelTypeRow1.map(opt => (
                <button
                  key={opt.value}
                  type="button"
                  disabled={isEdit}
                  onClick={() => setType(opt.value)}
                  className={`px-3 py-1.5 text-sm rounded-lg border transition-colors ${
                    type === opt.value
                      ? 'bg-primary text-white border-primary'
                      : 'bg-white text-gray-600 border-gray-300 hover:bg-gray-50'
                  } ${isEdit ? 'opacity-60 cursor-not-allowed' : ''}`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
            <div className="flex flex-wrap gap-2">
              {tunnelTypeRow2.map(opt => (
                <button
                  key={opt.value}
                  type="button"
                  disabled={isEdit}
                  onClick={() => setType(opt.value)}
                  className={`px-3 py-1.5 text-sm rounded-lg border transition-colors ${
                    type === opt.value
                      ? 'bg-primary text-white border-primary'
                      : 'bg-white text-gray-600 border-gray-300 hover:bg-gray-50'
                  } ${isEdit ? 'opacity-60 cursor-not-allowed' : ''}`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>
        </FormField>

        {/* HTTP/HTTPS 配置 */}
        {(type === 'http' || type === 'https') && (
          <>
            <div className="bg-blue-50 rounded-lg p-3 text-sm text-blue-700">
              将外部 HTTP/HTTPS 请求转发到本地 Web 服务，支持域名绑定和 WebSocket 长连接。
            </div>
            <FormField label="目标地址">
              <input
                type="text"
                value={target}
                onChange={e => setTarget(e.target.value)}
                placeholder="例如: http://127.0.0.1:8080"
                className={inputClass}
              />
            </FormField>
            <FormField label="域名（可选）">
              <input
                type="text"
                value={domain}
                onChange={e => setDomain(e.target.value)}
                placeholder="例如: app.example.com"
                className={inputClass}
              />
            </FormField>
          </>
        )}

        {/* TCP/UDP 配置 */}
        {(type === 'tcp' || type === 'udp') && (
          <>
            <div className="bg-blue-50 rounded-lg p-3 text-sm text-blue-700">
              在服务端监听端口，将 {type.toUpperCase()} 流量透明转发到节点本地目标地址。
            </div>
            <FormField label="目标地址（可选）">
              <input
                type="text"
                value={target}
                onChange={e => setTarget(e.target.value)}
                placeholder="例如: 127.0.0.1:8080"
                className={inputClass}
              />
            </FormField>
            <FormField label="监听端口">
              <input
                type="number"
                value={listenPort}
                onChange={e => setListenPort(e.target.value)}
                placeholder="例如: 8080"
                className={inputClass}
              />
            </FormField>
          </>
        )}

        {/* Ser2MQ 配置 */}
        {type === 'ser2mq' && (
          <>
            <div className="bg-blue-50 rounded-lg p-3 text-sm text-blue-700">
              串口数据通过 MQTT 协议双向转发，使用 ChaCha20-Poly1305 加密，客户端本地运行。
            </div>
            <FormField label="MQTT Broker">
              <input
                type="text"
                value={ser2mqBroker}
                onChange={e => setSer2mqBroker(e.target.value)}
                placeholder="mqtt://user:pass@broker:1883"
                className={inputClass}
              />
            </FormField>
            <FormField label="串口端口">
              <input
                type="text"
                value={ser2mqSerialPort}
                onChange={e => setSer2mqSerialPort(e.target.value)}
                placeholder="/dev/ttyS0"
                className={inputClass}
              />
            </FormField>
            <div className="grid grid-cols-2 gap-4">
              <FormField label="波特率">
                <select value={ser2mqBaudrate} onChange={e => setSer2mqBaudrate(e.target.value)} className={selectClass}>
                  {BAUDRATE_OPTIONS.map(v => <option key={v} value={v}>{v}</option>)}
                </select>
              </FormField>
              <FormField label="数据位">
                <select value={ser2mqDatabits} onChange={e => setSer2mqDatabits(e.target.value)} className={selectClass}>
                  {DATABITS_OPTIONS.map(v => <option key={v} value={v}>{v}</option>)}
                </select>
              </FormField>
            </div>
            <div className="grid grid-cols-3 gap-4">
              <FormField label="停止位">
                <select value={ser2mqStopbits} onChange={e => setSer2mqStopbits(e.target.value)} className={selectClass}>
                  {STOPBITS_OPTIONS.map(v => <option key={v} value={v}>{v}</option>)}
                </select>
              </FormField>
              <FormField label="校验位">
                <select value={ser2mqParity} onChange={e => setSer2mqParity(e.target.value)} className={selectClass}>
                  {PARITY_OPTIONS.map(v => <option key={v.value} value={v.value}>{v.label}</option>)}
                </select>
              </FormField>
              <FormField label="超时(ms)">
                <input
                  type="number"
                  value={ser2mqTimeout}
                  onChange={e => setSer2mqTimeout(e.target.value)}
                  min="100"
                  max="30000"
                  className={inputClass}
                />
              </FormField>
            </div>
            <FormField label="加密密钥（64 字符 hex）">
              <input
                type="text"
                value={ser2mqSecret}
                onChange={e => setSer2mqSecret(e.target.value)}
                placeholder="32 字节 hex 密钥，如 0123456789..."
                className={inputClass}
              />
              <p className="mt-1 text-xs text-gray-500">用于 ChaCha20-Poly1305 加密，长度必须为 64 字符</p>
            </FormField>
            <FormField label="QoS">
              <select
                value={ser2mqQoS}
                onChange={e => setSer2mqQoS(e.target.value)}
                className={selectClass}
              >
                <option value="0">0 — 最多一次</option>
                <option value="1">1 — 至少一次</option>
                <option value="2">2 — 恰好一次</option>
              </select>
            </FormField>
          </>
        )}

        {/* VPN Manager 配置 */}
        {type === 'vpn-manager' && (
          <>
            <div className="bg-blue-50 rounded-lg p-3 text-sm text-blue-700">
              管理 VPN 程序的启动、停止与运行监控，支持崩溃自动重启和日志采集。
            </div>
            <FormField label="程序名称">
              <input
                type="text"
                value={vpnBinaryName}
                onChange={e => setVpnBinaryName(e.target.value)}
                placeholder="easytier-core"
                className={inputClass}
              />
              <p className="mt-1 text-xs text-gray-500">程序查找顺序：./vnet/ → $PATH</p>
            </FormField>
            <FormField label="程序路径（可选）">
              <input
                type="text"
                value={vpnBinaryPath}
                onChange={e => setVpnBinaryPath(e.target.value)}
                placeholder="留空则自动查找"
                className={inputClass}
              />
            </FormField>
            <FormField label="启动参数（非 vnt-cli 程序使用）">
              <input
                type="text"
                value={vpnArgs}
                onChange={e => setVpnArgs(e.target.value)}
                placeholder="用空格分隔，如: -w udp://server:520/ -p 12345"
                className={inputClass}
              />
            </FormField>

            <div className="border-t border-gray-200 pt-4 mt-2">
              <h4 className="text-sm font-medium text-gray-700 mb-3">vnt-cli 配置</h4>
              <div className="space-y-4">
                <FormField label="连接令牌 (-k)">
                  <input
                    type="text"
                    value={vntToken}
                    onChange={e => setVntToken(e.target.value)}
                    placeholder="vnt组网令牌"
                    className={inputClass}
                  />
                </FormField>
                <div className="grid grid-cols-2 gap-4">
                  <FormField label="服务器地址 (-s)">
                    <input
                      type="text"
                      value={vntServer}
                      onChange={e => setVntServer(e.target.value)}
                      placeholder="vpn.example.com"
                      className={inputClass}
                    />
                  </FormField>
                  <FormField label="虚拟 IP (--ip)">
                    <input
                      type="text"
                      value={vntVirtualIP}
                      onChange={e => setVntVirtualIP(e.target.value)}
                      placeholder="10.99.0.15"
                      className={inputClass}
                    />
                  </FormField>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <FormField label="设备 ID (-d)">
                    <input
                      type="text"
                      value={vntDeviceID}
                      onChange={e => setVntDeviceID(e.target.value)}
                      placeholder="设备标识"
                      className={inputClass}
                    />
                  </FormField>
                  <FormField label="设备名称 (-n)">
                    <input
                      type="text"
                      value={vntDeviceName}
                      onChange={e => setVntDeviceName(e.target.value)}
                      placeholder="home-device"
                      className={inputClass}
                    />
                  </FormField>
                </div>
                <div className="grid grid-cols-3 gap-4">
                  <FormField label="密码 (-w)">
                    <input
                      type="text"
                      value={vntPassword}
                      onChange={e => setVntPassword(e.target.value)}
                      placeholder="可选"
                      className={inputClass}
                    />
                  </FormField>
                  <FormField label="输入代理 (-i)">
                    <input
                      type="text"
                      value={vntInIP}
                      onChange={e => setVntInIP(e.target.value)}
                      placeholder="可选"
                      className={inputClass}
                    />
                  </FormField>
                  <FormField label="输出代理 (-o)">
                    <input
                      type="text"
                      value={vntOutIP}
                      onChange={e => setVntOutIP(e.target.value)}
                      placeholder="可选"
                      className={inputClass}
                    />
                  </FormField>
                </div>
                <FormField label="REST API 端口">
                  <input
                    type="number"
                    value={vntRestPort}
                    onChange={e => setVntRestPort(e.target.value)}
                    min="1"
                    max="65535"
                    className={inputClass}
                  />
                  <p className="mt-1 text-xs text-gray-500">vnt-cli REST API 监听端口，默认 59871</p>
                </FormField>
              </div>
            </div>

            <div className="border-t border-gray-200 pt-4 mt-2">
              <h4 className="text-sm font-medium text-gray-700 mb-3">生命周期</h4>
              <div className="grid grid-cols-2 gap-4">
                <FormField label="最大重启次数">
                  <input
                    type="number"
                    value={vpnMaxRestarts}
                    onChange={e => setVpnMaxRestarts(e.target.value)}
                    min="0"
                    max="10"
                    className={inputClass}
                  />
                </FormField>
                <FormField label="重启延迟（秒）">
                  <input
                    type="number"
                    value={vpnRestartDelay}
                    onChange={e => setVpnRestartDelay(e.target.value)}
                    min="1"
                    max="60"
                    className={inputClass}
                  />
                </FormField>
              </div>
              <div className="space-y-2 mt-3">
                <FormField label="选项">
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={vpnAutostart}
                      onChange={e => setVpnAutostart(e.target.checked)}
                      className="w-4 h-4 text-primary rounded border-gray-300 focus:ring-primary"
                    />
                    <span className="text-sm text-gray-600">启动时自动运行</span>
                  </label>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={vpnRestartOnCrash}
                      onChange={e => setVpnRestartOnCrash(e.target.checked)}
                      className="w-4 h-4 text-primary rounded border-gray-300 focus:ring-primary"
                    />
                    <span className="text-sm text-gray-600">崩溃后自动重启</span>
                  </label>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={vpnLogCapture}
                      onChange={e => setVpnLogCapture(e.target.checked)}
                      className="w-4 h-4 text-primary rounded border-gray-300 focus:ring-primary"
                    />
                    <span className="text-sm text-gray-600">捕获日志输出</span>
                  </label>
                </FormField>
              </div>
              {vpnLogCapture && (
                <FormField label="日志文件路径（可选）">
                  <input
                    type="text"
                    value={vpnLogPath}
                    onChange={e => setVpnLogPath(e.target.value)}
                    placeholder="./vnet/logs/vpn.log"
                    className={inputClass}
                  />
                </FormField>
              )}
            </div>
          </>
        )}

        {/* Ser2Net 配置 (ser2tcp/ser2udp) */}
        {(type === 'ser2tcp' || type === 'ser2udp') && (
          <>
            <div className="bg-blue-50 rounded-lg p-3 text-sm text-blue-700">
              {snMode === 'server'
                ? `${type === 'ser2tcp' ? 'TCP Server' : 'UDP Server'}：监听本地端口，网络侧客户端可接入，串口数据双向转发。`
                : `${type === 'ser2tcp' ? 'TCP Client' : 'UDP Client'}：主动连接远端服务，适合把本地串口桥接到现有采集平台。`
              }
            </div>
            <div className="grid grid-cols-2 gap-4">
              <FormField label="模式">
                <select value={snMode} onChange={e => setSnMode(e.target.value)} className={selectClass}>
                  <option value="server">Server（本地监听）</option>
                  <option value="client">Client（主动连接）</option>
                </select>
              </FormField>
              <FormField label={snMode === 'server' ? '监听地址' : '远端地址'}>
                <input
                  type="text"
                  value={snAddress}
                  onChange={e => setSnAddress(e.target.value)}
                  placeholder={snMode === 'server' ? ':5000' : '192.168.1.100:5000'}
                  className={inputClass}
                />
              </FormField>
            </div>
            {type === 'ser2tcp' && snMode === 'server' && (
              <FormField label="最大连接数">
                <input
                  type="number"
                  value={snMaxConn}
                  onChange={e => setSnMaxConn(e.target.value)}
                  min="1"
                  max="100"
                  className={inputClass}
                />
              </FormField>
            )}

            <div className="border-t border-gray-200 pt-4 mt-2">
              <h4 className="text-sm font-medium text-gray-700 mb-3">串口参数</h4>
              <FormField label="串口端口">
                <input
                  type="text"
                  value={snSerialPort}
                  onChange={e => setSnSerialPort(e.target.value)}
                  placeholder="/dev/ttyS0"
                  className={inputClass}
                />
              </FormField>
              <div className="grid grid-cols-2 gap-4">
                <FormField label="波特率">
                  <select value={snBaudrate} onChange={e => setSnBaudrate(e.target.value)} className={selectClass}>
                    {BAUDRATE_OPTIONS.map(v => <option key={v} value={v}>{v}</option>)}
                  </select>
                </FormField>
                <FormField label="数据位">
                  <select value={snDatabits} onChange={e => setSnDatabits(e.target.value)} className={selectClass}>
                    {DATABITS_OPTIONS.map(v => <option key={v} value={v}>{v}</option>)}
                  </select>
                </FormField>
              </div>
              <div className="grid grid-cols-3 gap-4">
                <FormField label="停止位">
                  <select value={snStopbits} onChange={e => setSnStopbits(e.target.value)} className={selectClass}>
                    {STOPBITS_OPTIONS.map(v => <option key={v} value={v}>{v}</option>)}
                  </select>
                </FormField>
                <FormField label="校验位">
                  <select value={snParity} onChange={e => setSnParity(e.target.value)} className={selectClass}>
                    {PARITY_OPTIONS.map(v => <option key={v.value} value={v.value}>{v.label}</option>)}
                  </select>
                </FormField>
                <FormField label="超时(ms)">
                  <input
                    type="number"
                    value={snTimeout}
                    onChange={e => setSnTimeout(e.target.value)}
                    min="100"
                    max="30000"
                    className={inputClass}
                  />
                </FormField>
              </div>
            </div>
          </>
        )}

        {/* WebSSH 配置 */}
        {type === 'webssh' && (
          <>
            <div className="bg-blue-50 rounded-lg p-3 text-sm text-blue-700">
              通过 Web 浏览器访问远程 SSH 终端，支持密码和密钥认证。
            </div>
            <FormField label="SSH 主机">
              <input
                type="text"
                value={sshHost}
                onChange={e => setSshHost(e.target.value)}
                placeholder="例如: 192.168.1.100"
                className={inputClass}
              />
            </FormField>
            <div className="grid grid-cols-2 gap-4">
              <FormField label="SSH 端口">
                <input
                  type="number"
                  value={sshPort}
                  onChange={e => setSshPort(e.target.value)}
                  placeholder="22"
                  className={inputClass}
                />
              </FormField>
              <FormField label="用户名">
                <input
                  type="text"
                  value={sshUser}
                  onChange={e => setSshUser(e.target.value)}
                  placeholder="root"
                  className={inputClass}
                />
              </FormField>
            </div>
            <FormField label="认证方式">
              <select value={sshAuthType} onChange={e => setSshAuthType(e.target.value as 'password' | 'key')} className={`${selectClass} max-w-48`}>
                <option value="password">密码认证</option>
                <option value="key">密钥认证</option>
              </select>
            </FormField>
            {sshAuthType === 'password' ? (
              <FormField label="密码">
                <input
                  type="password"
                  value={sshPassword}
                  onChange={e => setSshPassword(e.target.value)}
                  placeholder="SSH 密码"
                  className={inputClass}
                />
              </FormField>
            ) : (
              <FormField label="私钥">
                <textarea
                  value={sshPrivKey}
                  onChange={e => setSshPrivKey(e.target.value)}
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                  className={inputClass + ' h-32 font-mono text-xs'}
                />
              </FormField>
            )}
          </>
        )}

        <FormField label="节点">
          <select
            value={nodeId}
            onChange={e => setNodeId(e.target.value)}
            disabled={nodesLoading || !!presetNodeId}
            className={selectClass}
          >
            <option value="">{nodesLoading ? '加载中...' : '选择节点'}</option>
            {(isEdit ? allNodes : onlineNodes).map(node => (
              <option key={node.id} value={node.id}>
                {node.name} ({node.id.slice(0, 8)}{node.status === 'online' ? '' : ', 离线'})
              </option>
            ))}
          </select>
          {!nodesLoading && (isEdit ? allNodes : onlineNodes).length === 0 && (
            <p className="mt-1 text-xs text-amber-600">暂无{isEdit ? '' : '在线'}节点</p>
          )}
        </FormField>

        {/* 限速配置（仅标准类型） */}
        {isStandardType && (
          <div className="border-t border-gray-200 pt-4 mt-2">
            <h4 className="text-sm font-medium text-gray-700 mb-3">限速配置（可选）</h4>
            <div className="space-y-4">
              <FormField label="最大连接数">
                <input
                  type="number"
                  value={rlMaxConns}
                  onChange={e => setRlMaxConns(e.target.value)}
                  placeholder="留空 = 使用全局默认"
                  min="1"
                  max="100000"
                  className={inputClass}
                />
              </FormField>
              <FormField label="最大带宽">
                <div className="flex gap-2">
                  <input
                    type="text"
                    inputMode="numeric"
                    value={rlMaxBandwidth}
                    onChange={e => setRlMaxBandwidth(e.target.value)}
                    placeholder="留空 = 使用全局默认"
                    className={`${inputClass} flex-1 min-w-[80px]`}
                  />
                  <select
                    value={rlBwUnit}
                    onChange={e => setRlBwUnit(e.target.value as 'bps' | 'kbps' | 'mbps')}
                    className="px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary shrink-0"
                  >
                    <option value="kbps">KB/s</option>
                    <option value="mbps">MB/s</option>
                    <option value="bps">B/s</option>
                  </select>
                </div>
              </FormField>
            </div>
            {isEdit && tunnel?.effective_max_conns ? (
              <p className="mt-1 text-xs text-gray-400">
                当前生效：{tunnel.effective_max_conns} 连接 / {tunnel.effective_max_bandwidth ? formatBandwidth(tunnel.effective_max_bandwidth) : '不限'}
              </p>
            ) : null}
          </div>
        )}

        <FormField label="启用">
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={enabled}
              onChange={e => setEnabled(e.target.checked)}
              className="w-4 h-4 text-primary rounded border-gray-300 focus:ring-primary"
            />
            <span className="text-sm text-gray-600">启用此隧道</span>
          </label>
        </FormField>

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
          >
            取消
          </button>
          <button
            type="submit"
            disabled={submitting}
            className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
          >
            {submitting ? '提交中...' : isEdit ? '保存' : '创建'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
