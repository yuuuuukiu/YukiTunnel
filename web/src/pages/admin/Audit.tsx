import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Download, FileClock, RefreshCw } from 'lucide-react';
import { api } from '../../lib/api';
import { Badge, Button, Card, CardHeader, Spinner } from '../../components/ui';
import { formatTime } from '../../lib/format';

const TONE: Record<string, 'good' | 'warn' | 'bad' | 'info' | 'neutral'> = {
  auth: 'info',
  user: 'good',
  node: 'good',
  tunnel: 'neutral',
  settings: 'warn',
  key: 'info',
  token: 'info',
  admin: 'warn',
};

export default function AuditPage() {
  const [limit, setLimit] = useState(100);
  const [offset, setOffset] = useState(0);
  const [action, setAction] = useState('');
  const audit = useQuery({ queryKey: ['audit', limit, offset], queryFn: () => api.audit(limit, offset), refetchInterval: 15000 });

  const entries = (audit.data?.entries ?? []).filter((e) => !action || e.action.startsWith(action));

  const exportJSON = () => {
    const blob = new Blob([JSON.stringify(entries, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `yukitunnel-audit-${new Date().toISOString().slice(0, 19)}.json`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>审计日志</h1>
          <p>记录登录、用户、节点、隧道、域名与端口设置的变更，用于追责与排障。</p>
        </div>
        <div className="row gap-8">
          <Button variant="subtle" size="md" onClick={() => void audit.refetch()}><RefreshCw size={15} /> 刷新</Button>
          <Button variant="subtle" size="md" onClick={exportJSON}><Download size={15} /> 导出 JSON</Button>
        </div>
      </div>

      <Card>
        <CardHeader
          title="操作记录"
          subtitle={`第 ${offset + 1} - ${offset + entries.length} 条`}
          actions={
            <select value={action} onChange={(e) => setAction(e.target.value)} style={{ width: 170 }}>
              <option value="">全部动作</option>
              <option value="auth">认证</option>
              <option value="user">用户</option>
              <option value="key">密钥</option>
              <option value="token">令牌</option>
              <option value="node">节点</option>
              <option value="tunnel">隧道</option>
              <option value="settings">设置</option>
            </select>
          }
        />
        {audit.isLoading ? (
          <Spinner />
        ) : entries.length === 0 ? (
          <div className="empty"><FileClock size={26} /><p>暂无记录</p></div>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>时间</th>
                  <th>操作人</th>
                  <th>动作</th>
                  <th>对象</th>
                  <th>详情</th>
                  <th>来源 IP</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e) => (
                  <tr key={e.id}>
                    <td className="small mono">{formatTime(e.created_at)}</td>
                    <td className="small">{e.actor_name || '系统'}</td>
                    <td><Badge tone={TONE[e.action.split('.')[0]] ?? 'neutral'}>{e.action}</Badge></td>
                    <td className="small mono">{e.target_type}{e.target_id ? ':' + e.target_id : ''}</td>
                    <td className="small muted">{e.detail || '—'}</td>
                    <td className="small mono">{e.ip}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="row gap-8" style={{ marginTop: 14 }}>
          <Button variant="ghost" size="sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>上一页</Button>
          <Button variant="ghost" size="sm" disabled={entries.length < limit} onClick={() => setOffset(offset + limit)}>下一页</Button>
          <span className="grow" />
          <select value={limit} onChange={(e) => { setLimit(Number(e.target.value)); setOffset(0); }} style={{ width: 110 }}>
            <option value={50}>50 条</option>
            <option value={100}>100 条</option>
            <option value={200}>200 条</option>
          </select>
        </div>
      </Card>
    </>
  );
}
