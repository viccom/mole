import { useMemo, useState } from 'react'
import { api } from '../api/client'
import type { Node, Tunnel, TunnelPara, SerialConfig, BinaryConfig, LifecycleConfig, WatchdogConfig, LogConfig } from '../types/api'
import { Modal } from './Modal'
import { FormField } from './FormField'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'

interface TunnelFormModalProps {
  tunnel?: Tunnel
  presetNodeId?: string
  onClose: () => void
  onSuccess: () => void
}

type TunnelType = 'http' | 'https' | 'tcp' | 'udp' | 'ser2mq' | 'vpn-manager'

export function TunnelFormModal({ tunnel, presetNodeId, onClose, onSuccess }: TunnelFormModalProps) {
  const { toast } = useToast()
  const isEdit = !!tunnel

  const [name, setName] = useState(tunnel?.name || '')
  const [type, setType] = useState<TunnelType>(tunnel?.type || 'http')
  const [target, setTarget] = useState(tunnel?.target || '')
  const [domain, setDomain] = useState(tunnel?.domain || '')
  const [listenPort, setListenPort] = useState(tunnel?.listen_port?.toString() || '')
  const [nodeId, setNodeId] = useState(presetNodeId || tunnel?.node_id || '')
  const [enabled, setEnabled] = useState(tunnel?.enabled !== false)

  // ser2mq 配置
  const [ser2mqBroker, setSer2mqBroker] = useState(tunnel?.para?.broker || '')
  const [ser2mqSerialPort, setSer2mqSerialPort] = useState(tunnel?.para?.serial?.port || '/dev/ttyS0')
  const [ser2mqBaudrate, setSer2mqBaudrate] = useState(tunnel?.para?.serial?.baudrate?.toString() || '9600')
  const [ser2mqDatabits, setSer2mqDatabits] = useState(tunnel?.para?.serial?.databits?.toString() || '8')
  const [ser2mqParity, setSer2mqParity] = useState(tunnel?.para?.serial?.parity || 'N')
  const [ser2mqSecret, setSer2mqSecret] = useState(tunnel?.para?.secret || '')

  // vpn-manager 配置
  const [vpnBinaryName, setVpnBinaryName] = useState(tunnel?.para?.binary?.name || '')
  const [vpnBinaryPath, setVpnBinaryPath] = useState(tunnel?.para?.binary?.path || '')
  const [vpnArgs, setVpnArgs] = useState(tunnel?.para?.args?.join(' ') || '')
  const [vpnAutostart, setVpnAutostart] = useState(tunnel?.para?.lifecycle?.autostart || false)
  const [vpnRestartOnCrash, setVpnRestartOnCrash] = useState(tunnel?.para?.lifecycle?.restart_on_crash ?? true)
  const [vpnMaxRestarts, setVpnMaxRestarts] = useState(tunnel?.para?.lifecycle?.max_restarts?.toString() || '3')
  const [vpnRestartDelay, setVpnRestartDelay] = useState(tunnel?.para?.lifecycle?.restart_delay?.toString() || '5')
  const [vpnLogCapture, setVpnLogCapture] = useState(tunnel?.para?.log?.capture ?? true)
  const [vpnLogMaxSize, setVpnLogMaxSize] = useState(tunnel?.para?.log?.max_size?.toString() || '65536')
  const [vpnLogPath, setVpnLogPath] = useState(tunnel?.para?.log?.output_path || '')

  const [submitting, setSubmitting] = useState(false)

  const { data: nodesResponse, loading: nodesLoading } = useRequest(
    () => api.getNodes(),
    { onError: () => {} },
  )
  const onlineNodes = useMemo(
    () => (nodesResponse?.items || []).filter((node: Node) => node.status === 'online'),
    [nodesResponse],
  )

  const buildPara = (): TunnelPara | undefined => {
    if (type === 'ser2mq') {
      return {
        broker: ser2mqBroker,
        serial: {
          port: ser2mqSerialPort,
          baudrate: Number(ser2mqBaudrate),
          databits: Number(ser2mqDatabits),
          parity: ser2mqParity,
          timeout: 3000,
        },
        secret: ser2mqSecret,
      }
    }
    if (type === 'vpn-manager') {
      return {
        binary: {
          name: vpnBinaryName,
          path: vpnBinaryPath,
        },
        args: vpnArgs.split(' ').filter(s => s.trim()),
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
    if (!isEdit && !nodeId) {
      toast('请选择节点', 'error')
      return
    }

    try {
      setSubmitting(true)
      // 构建 payload，支持所有隧道类型
      const payload = {
        name: name.trim(),
        type,
        enabled,
        node_id: nodeId,
        target: (type === 'http' || type === 'https') ? target.trim()
            : (type === 'tcp' || type === 'udp') ? (target.trim() || '127.0.0.1:0')
            : (type === 'ser2mq') ? ser2mqSerialPort
            : (type === 'vpn-manager') ? vpnBinaryName
            : target.trim(),
        domain: (type === 'http' || type === 'https') && domain.trim() ? domain.trim() : undefined,
        listen_port: (type === 'tcp' || type === 'udp') ? Number(listenPort) : undefined,
        para: (type === 'ser2mq' || type === 'vpn-manager') ? buildPara() : undefined,
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
    <Modal title={isEdit ? '编辑隧道' : '创建隧道'} onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4 max-h-[70vh] overflow-y-auto">
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
          <select
            value={type}
            onChange={e => setType(e.target.value as TunnelType)}
            className={selectClass}
          >
            <option value="http">HTTP</option>
            <option value="https">HTTPS</option>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="ser2mq">Ser2MQ（串口转 MQTT）</option>
            <option value="vpn-manager">VPN Manager（程序管理）</option>
          </select>
        </FormField>

        {/* HTTP/HTTPS 配置 */}
        {(type === 'http' || type === 'https') && (
          <>
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
                <select
                  value={ser2mqBaudrate}
                  onChange={e => setSer2mqBaudrate(e.target.value)}
                  className={selectClass}
                >
                  <option value="1200">1200</option>
                  <option value="2400">2400</option>
                  <option value="4800">4800</option>
                  <option value="9600">9600</option>
                  <option value="19200">19200</option>
                  <option value="38400">38400</option>
                  <option value="57600">57600</option>
                  <option value="115200">115200</option>
                </select>
              </FormField>
              <FormField label="数据位">
                <select
                  value={ser2mqDatabits}
                  onChange={e => setSer2mqDatabits(e.target.value)}
                  className={selectClass}
                >
                  <option value="5">5</option>
                  <option value="6">6</option>
                  <option value="7">7</option>
                  <option value="8">8</option>
                </select>
              </FormField>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <FormField label="校验位">
                <select
                  value={ser2mqParity}
                  onChange={e => setSer2mqParity(e.target.value)}
                  className={selectClass}
                >
                  <option value="N">无校验 (N)</option>
                  <option value="E">偶校验 (E)</option>
                  <option value="O">奇校验 (O)</option>
                </select>
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
          </>
        )}

        {/* VPN Manager 配置 */}
        {type === 'vpn-manager' && (
          <>
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
            <FormField label="启动参数">
              <input
                type="text"
                value={vpnArgs}
                onChange={e => setVpnArgs(e.target.value)}
                placeholder="用空格分隔，如: -w udp://server:520/ -p 12345"
                className={inputClass}
              />
            </FormField>
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
            <div className="space-y-2">
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
          </>
        )}

        {!isEdit && (
          <FormField label="节点">
            <select
              value={nodeId}
              onChange={e => setNodeId(e.target.value)}
              disabled={nodesLoading || !!presetNodeId}
              className={selectClass}
            >
              <option value="">{nodesLoading ? '加载中...' : '选择节点'}</option>
              {onlineNodes.map(node => (
                <option key={node.id} value={node.id}>
                  {node.name} ({node.id.slice(0, 8)})
                </option>
              ))}
            </select>
            {!nodesLoading && onlineNodes.length === 0 && (
              <p className="mt-1 text-xs text-amber-600">暂无在线节点</p>
            )}
          </FormField>
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
