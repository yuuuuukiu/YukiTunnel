import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Globe2, Save, ShieldAlert } from 'lucide-react';
import { api } from '../../lib/api';
import { useToast } from '../../state/toast';
import { Button, Card, CardHeader, Field, Spinner, Switch } from '../../components/ui';

export default function AdminSettingsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const settings = useQuery({ queryKey: ['admin-settings'], queryFn: api.settings });

  const [binding, setBinding] = useState(false);
  const [suffix, setSuffix] = useState('');
  const [publicHost, setPublicHost] = useState('');
  const [portMin, setPortMin] = useState('20000');
  const [portMax, setPortMax] = useState('30000');

  useEffect(() => {
    if (!settings.data) return;
    const s = settings.data.settings;
    setBinding(s.domain_binding);
    setSuffix(s.domain_suffix);
    setPublicHost(s.public_host);
    setPortMin(String(s.port_min));
    setPortMax(String(s.port_max));
  }, [settings.data]);

  const save = useMutation({
    mutationFn: () =>
      api.updateSettings({
        domain_binding: binding,
        domain_suffix: suffix.trim(),
        public_host: publicHost.trim(),
        port_min: Number(portMin),
        port_max: Number(portMax),
      }),
    onSuccess: () => {
      toast.success('设置已保存', '新建隧道将立即使用新设置');
      void qc.invalidateQueries({ queryKey: ['admin-settings'] });
      void qc.invalidateQueries({ queryKey: ['servers'] });
    },
    onError: (e) => toast.error('保存失败', e instanceof Error ? e.message : ''),
  });

  if (settings.isLoading) return <Spinner label="加载设置…" />;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>域名与端口</h1>
          <p>这两个开关决定新建隧道返回的地址形式与可分配的公网端口。</p>
        </div>
        <Button onClick={() => save.mutate()} disabled={save.isPending}><Save size={15} /> 保存设置</Button>
      </div>

      <div className="grid grid-2">
        <Card>
          <CardHeader title="域名绑定" subtitle="开启后新建隧道返回「域名 + 端口」；关闭则返回「公网 IP + 端口」" />
          <div className="row gap-12" style={{ marginBottom: 14 }}>
            <Switch checked={binding} onChange={setBinding} label="域名绑定" />
            <div>
              <div style={{ fontWeight: 600 }}>{binding ? '已开启' : '已关闭'}</div>
              <div className="faint small">
                {binding ? '客户端将看到域名地址，需要把 DNS 指向节点。' : '客户端将看到公网 IP 地址。'}
              </div>
            </div>
          </div>
          <Field label="域名后缀" hint="例如 tunnel.example.com，系统会生成 <隧道名>-<随机>.tunnel.example.com">
            <input value={suffix} onChange={(e) => setSuffix(e.target.value)} placeholder="tunnel.example.com" disabled={!binding} />
          </Field>
          <div className="alert alert-info" style={{ marginTop: 14 }}>
            <Globe2 size={16} />
            <div className="small">
              建议为该后缀配置泛解析 <span className="mono">*.{suffix || 'tunnel.example.com'}</span> → 节点公网 IP。
              同时需要在节点上放行对应端口。
            </div>
          </div>
        </Card>

        <Card>
          <CardHeader title="端口范围" subtitle="自动分配公网端口时只会在此范围内选择" />
          <div className="grid grid-2">
            <Field label="起始端口">
              <input value={portMin} onChange={(e) => setPortMin(e.target.value.replace(/\D/g, ''))} />
            </Field>
            <Field label="结束端口">
              <input value={portMax} onChange={(e) => setPortMax(e.target.value.replace(/\D/g, ''))} />
            </Field>
          </div>
          <Field label="公网入口地址（可选）" hint="留空时使用节点的公网入口；填写后覆盖所有节点的展示地址">
            <input value={publicHost} onChange={(e) => setPublicHost(e.target.value)} placeholder="自动" />
          </Field>
          <div className="alert alert-warn" style={{ marginTop: 14 }}>
            <ShieldAlert size={16} />
            <div className="small">端口范围应与防火墙放行范围一致，否则用户拿到地址也无法访问。</div>
          </div>
        </Card>
      </div>

      <Card>
        <CardHeader title="服务端信息" subtitle="只读，修改请编辑 config.yaml 并重启" />
        <dl className="kv">
          <dt>版本</dt><dd>{settings.data?.settings.version}</dd>
          <dt>API 监听</dt><dd className="mono">{settings.data?.settings.api_addr}</dd>
          <dt>控制监听</dt><dd className="mono">{settings.data?.settings.control_addr}</dd>
        </dl>
      </Card>
    </>
  );
}
