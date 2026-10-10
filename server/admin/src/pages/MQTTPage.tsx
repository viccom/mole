import { useState } from 'react'
import { api } from '../api/client'
import type { MQTTStats, MQTTClient, MQTTTopic } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { StatCard } from '../components/StatCard'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'
import { Users, BookOpen, Send } from 'lucide-react'

// MQTT API may return PascalCase fields; normalize them
function normalizeClient(c: Record<string, unknown>): { client_id: string; username: string } {
  return {
    client_id: (c.client_id || c.ID || c.ClientID || '') as string,
    username: (c.username || c.Username || '') as string,
  }
}

function normalizeTopic(t: Record<string, unknown>): { topic: string; qos: number; subscribers: number } {
  return {
    topic: (t.topic || t.Topic || t.filter || t.Filter || '') as string,
    qos: (t.qos ?? t.QoS ?? 0) as number,
    subscribers: (t.subscribers ?? t.Subscribers ?? t.subscription_count ?? 0) as number,
  }
}

function asRecordList(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value as Record<string, unknown>[] : []
}

export function MQTTPage() {
  const { toast } = useToast()
  // Publish form
  const [pubTopic, setPubTopic] = useState('')
  const [pubPayload, setPubPayload] = useState('')
  const [publishing, setPublishing] = useState(false)
  const { data, loading, run: fetchData } = useRequest(async () => {
    const [statsRes, clientsRes, topicsRes] = await Promise.all([
      api.getMQTTStats().catch(() => null as MQTTStats | null),
      api.getMQTTClients().catch(() => [] as MQTTClient[]),
      api.getMQTTTopics().catch(() => [] as MQTTTopic[]),
    ])

    return {
      stats: statsRes,
      clients: asRecordList(clientsRes).map(normalizeClient),
      topics: asRecordList(topicsRes).map(normalizeTopic),
    }
  })

  const stats = data?.stats || null
  const clients = data?.clients || []
  const topics = data?.topics || []

  const handlePublish = async () => {
    if (!pubTopic.trim()) {
      toast('请输入 Topic', 'error')
      return
    }
    setPublishing(true)
    try {
      await api.publishMQTT(pubTopic.trim(), pubPayload, 0, false)
      toast('消息已发布', 'success')
      setPubTopic('')
      setPubPayload('')
      fetchData()
    } catch (err: unknown) {
      // 透传后端原因（如 topic 非法），不能只报 generic 文案
      toast((err as Error).message || '发布失败', 'error')
    } finally {
      setPublishing(false)
    }
  }

  if (loading) return <Loading />

  return (
    <>
      <PageHeader title="MQTT" />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        {/* Stat cards */}
        <div className="grid grid-cols-3 gap-4">
          <StatCard
            icon={Users}
            iconBg="bg-emerald-100"
            iconColor="text-emerald-600"
            value={String(stats?.clients_connected ?? '-')}
            label="在线客户端"
          />
          <StatCard
            icon={BookOpen}
            iconBg="bg-blue-100"
            iconColor="text-blue-600"
            value={String(stats?.subscriptions ?? '-')}
            label="订阅数"
          />
          <StatCard
            icon={Send}
            iconBg="bg-purple-100"
            iconColor="text-purple-600"
            value={String(stats?.messages_published ?? '-')}
            label="已发布"
          />
        </div>

        {/* Publish message */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200">
            <h3 className="font-semibold text-gray-900">发布消息</h3>
          </div>
          <div className="p-5 space-y-3">
            <div>
              <input
                type="text"
                value={pubTopic}
                onChange={e => setPubTopic(e.target.value)}
                placeholder="Topic"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary"
              />
            </div>
            <div>
              <textarea
                value={pubPayload}
                onChange={e => setPubPayload(e.target.value)}
                placeholder="Payload"
                rows={3}
                className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary resize-none"
              />
            </div>
            <div className="flex justify-end">
              <button
                onClick={handlePublish}
                disabled={publishing}
                className="inline-flex items-center gap-1.5 px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
              >
                <Send className="w-4 h-4" />
                {publishing ? '发布中...' : '发布'}
              </button>
            </div>
          </div>
        </div>

        {/* Two-column: Clients & Topics */}
        <div className="grid grid-cols-2 gap-6">
          {/* Clients table */}
          <div className="bg-white rounded-lg shadow-sm">
            <div className="px-5 py-4 border-b border-gray-200">
              <h3 className="font-semibold text-gray-900">客户端</h3>
            </div>
            {clients.length === 0 ? (
              <Empty message="暂无客户端" />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full">
                  <thead>
                    <tr className="bg-gray-50">
                      <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">Client ID</th>
                      <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">用户名</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {clients.map((c, i) => (
                      <tr key={i} className="hover:bg-gray-50/50">
                        <td className="px-4 py-3">
                          <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{c.client_id || '-'}</code>
                        </td>
                        <td className="px-4 py-3 text-gray-500 text-sm">{c.username || '-'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          {/* Topics table */}
          <div className="bg-white rounded-lg shadow-sm">
            <div className="px-5 py-4 border-b border-gray-200">
              <h3 className="font-semibold text-gray-900">订阅主题</h3>
            </div>
            {topics.length === 0 ? (
              <Empty message="暂无订阅" />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full">
                  <thead>
                    <tr className="bg-gray-50">
                      <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">Topic</th>
                      <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">QoS</th>
                      <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">订阅数</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {topics.map((t, i) => (
                      <tr key={i} className="hover:bg-gray-50/50">
                        <td className="px-4 py-3">
                          <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{t.topic || '-'}</code>
                        </td>
                        <td className="px-4 py-3 text-sm">{t.qos}</td>
                        <td className="px-4 py-3 text-sm">{t.subscribers}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      </div>
    </>
  )
}
