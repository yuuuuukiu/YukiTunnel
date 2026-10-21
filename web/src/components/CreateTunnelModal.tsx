import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, Copy, Globe2, Loader2, Network } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import type { Proto, Tunnel } from '../lib/types';
import { Badge, Button, Field, Modal, Spinner } from './ui';
import { copyText, latencyClass } from '../lib/format';
import { useToast } from '../state/toast';

const PROTOCOLS: { value: Proto; label: string; hint: string }[] = [
  { value: 'tcp', label: 'TCP', hint: 'SSH、数据库、游戏等' },
  { value: 'udp', label: 'UDP', hint: 'DNS、语音、联机游戏' },
  { value: 'http', label: 'HTTP', hint: '网站（服务端解析 Host）' },
  { value: 'https', label: 'HTTPS', hint: '本地自带证书的 TLS 服务' },
];

export function CreateTunnelModal({
  open,
  onClose,
  onCreated,
  initial,
}: {
  open: boolean;
  onClose: () => void;
  onCreated?: (t: Tunnel) => void;
  initial?: Tunnel | null;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, enabled: open });

  const [nodeId, setNodeId] = useState('');
  const [proto, setProto] = useState<Proto>('tcp');
  const [localIP, setLocalIP] = useState('127.0.0.1');
  const [localPort, setLocalPort] = useState('');
  const [name, setName] = useState('');
  const [publicPort, setPublicPort] = useState('');
  const [useDomain, setUseDomain] = useState(true);
  const [error, setError] = useState('');
  const [result, setResult] = useState<Tunnel | null>(null);

  const binding = servers.data?.domain_binding ?? false;

  useEffect(() => {
    if (!open) return;
    setError('');
    setResult(null);
    if (initial) {
      setNodeId(initial.node_id);
      setProto(initial.proto);
      setLocalIP(initial.local_ip);
      setLocalPort(String(initial.local_port));
      setName(initial.name);
      setPublicPort(String(initial.public_port || ''));
      setUseDomain(Boolean(initial.domain));
    } else {
      setName('');
      setLocalPort('');
      setPublicPort('');
      setUseDomain(binding);
      const firstOnline = servers.data?.servers.find((s) => s.online) ?? servers.data?.servers[0];
      setNodeId((prev) => prev || firstOnline?.id || '');
    }
  }, [open, initial, binding, servers.data]);

  const selected = useMemo(
    () => servers.data?.servers.find((s) => s.id === nodeId),
    [servers.data, nodeId],
  );

  const mutation = useMutation({
    mutationFn: async () => {
      const port = Number(localPort);
      if (!nodeId) throw new Error('请选择节点');
      if (!localIP.trim()) throw new Error('请填写内网 IP 或主机名');
      if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('内网端口需为 1-65535 的整数');
      const body = {
        name: name.trim() || undefined,
        node_id: nodeId,
        proto,
        local_ip: localIP.trim(),
        local_port: port,
        public_port: publicPort ? Number(publicPort) : undefined,
        use_domain: binding ? useDomain : false,
      };
      if (initial) {
        const res = await api.updateTunnel(initial.id, {
          name: body.name,
          node_id: body.node_id,
          proto: body.proto,
          local_ip: body.local_ip,
          local_port: body.local_port,
          public_port: body.public_port,
        });
        return res.tunnel;
      }
      const res = await api.createTunnel(body);
      return res.tunnel;
    },
    onSuccess: (t) => {
      void qc.invalidateQueries({ queryKey: ['tunnels'] });
      void qc.invalidateQueries({ queryKey: ['servers'] });
      if (initial) {
        toast.success('隧道已更新');
        onCreated?.(t);
        onClose();
        return;
      }
      setResult(t);
      toast.success('隧道创建成功', t.address);
      onCreated?.(t);
    },
    onError: (err) => setError(err instanceof ApiError || err instanceof Error ? err.message : '创建失败'),
  });

  const close = () => {
    setResult(null);
    onClose();
  };

  if (result) {
    return (
      <Modal
        open={open}
        title="隧道创建成功"
        subtitle="把这个公网地址分享给需要访问内网服务的人"
        onClose={close}
        footer={
          <>
            <Button variant="ghost" onClick={close}>完成</Button>
            <Button
              onClick={() => {
                close();
                void api.startTunnel(result.id).then(() => {
                  void qc.invalidateQueries({ queryKey: ['tunnels'] });
                  toast.success('隧道已启动', result.address);
                }).catch((e: unknown) => {
                  toast.error('启动失败', e instanceof Error ? e.message : '未知错误');
                });
              }}
            >
              立即启动
            </Button>
          </>
        }
      >
        <div className="alert alert-info">
          <CheckCircle2 size={18} />
          <div>
            <strong>{result.name}</strong>
            <div className="small">{result.proto.toUpperCase()} · {result.local_ip}:{result.local_port} · 节点 {result.node_name}</div>
          </div>
        </div>
        <div className="card" style={{ padding: 14 }}>
          <div className="faint small" style={{ marginBottom: 6 }}>公网访问地址</div>
          <div className="row gap-8 wrap">
            <span className="addr" style={{ fontSize: 17 }}>{result.address}</span>
            <Button
              size="sm"
              variant="subtle"
              onClick={() => {
                void copyText(result.address);
                toast.success('已复制地址');
              }}
            >
              <Copy size={13} /> 复制
            </Button>
          </div>
          <div className="faint small" style={{ marginTop: 8 }}>
            {result.domain ? (
              <>域名绑定已开启，请确保 <span className="mono">{result.domain}</span> 解析到该节点。</>
            ) : (
              <>域名绑定已关闭，使用「公网 IP + 端口」形式访问。</>
            )}
          </div>
        </div>
      </Modal>
    );
  }

  return (
    <Modal
      open={open}
      title={initial ? '编辑隧道' : '新建隧道'}
      subtitle={initial ? '修改后若隧道正在运行会自动重启' : '选择节点、填写内网服务地址即可创建'}
      onClose={close}
      footer={
        <>
          <Button variant="ghost" onClick={close}>取消</Button>
          <Button onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 size={15} className="spinner-icon" /> : <Globe2 size={15} />}
            {mutation.isPending ? '提交中…' : initial ? '保存' : '创建隧道'}
          </Button>
        </>
      }
    >
      {error && <div className="alert alert-bad">{error}</div>}
      <Field label="穿透节点" hint="延迟越低访问越快；离线节点无法创建隧道">
        {servers.isLoading ? (
          <Spinner label="加载节点…" />
        ) : (
          <div className="node-picker">
            {servers.data?.servers.map((s) => (
              <button
                key={s.id}
                type="button"
                className={`node-option ${nodeId === s.id ? 'selected' : ''}`}
                onClick={() => setNodeId(s.id)}
              >
                <div className="row gap-8">
                  <Network size={15} />
                  <strong>{s.name}</strong>
                  {s.online ? <Badge tone="good">在线</Badge> : <Badge tone="bad">离线</Badge>}
                </div>
                <div className="faint small">{s.region || '默认区域'} · {s.public_host}</div>
                <div className={`latency latency-${latencyClass(s.latency_ms)}`}>{s.latency_ms} ms</div>
              </button>
            ))}
            {(servers.data?.servers.length ?? 0) === 0 && (
              <div className="alert alert-warn">服务端尚未配置可用节点，请联系管理员。</div>
            )}
          </div>
        )}
      </Field>

      <Field label="协议">
        <div className="proto-grid">
          {PROTOCOLS.map((p) => (
            <button
              key={p.value}
              type="button"
              className={`proto-option ${proto === p.value ? 'selected' : ''}`}
              onClick={() => {
                setProto(p.value);
                if (!localPort) setLocalPort(p.value === 'https' ? '443' : p.value === 'http' ? '80' : '');
              }}
            >
              <strong>{p.label}</strong>
              <span className="faint small">{p.hint}</span>
            </button>
          ))}
        </div>
      </Field>

      <div className="grid grid-2">
        <Field label="内网 IP / 主机名" hint="本地服务所在地址，通常为 127.0.0.1">
          <input value={localIP} onChange={(e) => setLocalIP(e.target.value)} placeholder="127.0.0.1" />
        </Field>
        <Field label="内网端口">
          <input
            value={localPort}
            onChange={(e) => setLocalPort(e.target.value.replace(/[^0-9]/g, ''))}
            placeholder="8080"
            inputMode="numeric"
          />
        </Field>
      </div>

      <div className="grid grid-2">
        <Field label="隧道名称（可选）">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 家里 NAS" />
        </Field>
        <Field label="公网端口（可选）" hint={selected ? `范围 ${selected.port_range[0]}-${selected.port_range[1]}，留空自动分配` : '留空自动分配'}>
          <input
            value={publicPort}
            onChange={(e) => setPublicPort(e.target.value.replace(/[^0-9]/g, ''))}
            placeholder="自动"
            inputMode="numeric"
          />
        </Field>
      </div>

      <Field label="公网地址形式">
        {binding ? (
          <div className="radio-row">
            <label>
              <input type="radio" checked={useDomain} onChange={() => setUseDomain(true)} /> 域名 + 端口
            </label>
            <label>
              <input type="radio" checked={!useDomain} onChange={() => setUseDomain(false)} /> IP + 端口
            </label>
          </div>
        ) : (
          <div className="alert alert-info small">管理员已关闭域名绑定，隧道将返回「公网 IP + 端口」地址。</div>
        )}
      </Field>
    </Modal>
  );
}
