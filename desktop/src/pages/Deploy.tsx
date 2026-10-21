import { useCallback, useEffect, useState } from 'react';
import {
  AlertTriangle, CheckCircle2, ChevronLeft, ChevronRight, Copy, ExternalLink, FolderOpen,
  Loader2, Play, RefreshCw, Rocket, Server, ShieldCheck, Square, Terminal,
} from 'lucide-react';
import { Badge, Button, Card, CardHeader, Field, Spinner, Switch } from '@web/components/ui';
import { useNavigate } from 'react-router-dom';
import { useToast } from '@web/state/toast';
import { copyText } from '@web/lib/format';
import {
  desktop, isDesktop, listen,
  type DeployRequest, type DeployResult, type EnvReport, type Progress, type ServerStatus,
} from '../lib/tauri';

type Step = 0 | 1 | 2 | 3 | 4;

const STEPS = ['环境检测', '网络与端口', '安全与管理员', '确认部署', '完成'];

interface FormState {
  dataDir: string;
  apiPort: string;
  controlPort: string;
  publicHost: string;
  adminUser: string;
  adminPassword: string;
  domainBinding: boolean;
  domainSuffix: string;
  portMin: string;
  portMax: string;
  tlsMode: 'self-signed' | 'existing';
  tlsCert: string;
  tlsKey: string;
}

const initialForm: FormState = {
  dataDir: '',
  apiPort: '8443',
  controlPort: '9443',
  publicHost: '',
  adminUser: 'admin',
  adminPassword: '',
  domainBinding: false,
  domainSuffix: '',
  portMin: '20000',
  portMax: '20100',
  tlsMode: 'self-signed',
  tlsCert: '',
  tlsKey: '',
};

/** The one-click installer wizard for the embedded server. */
export default function DeployPage() {
  const toast = useToast();
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>(0);
  const [form, setForm] = useState<FormState>(initialForm);
  const [env, setEnv] = useState<EnvReport | null>(null);
  const [envBusy, setEnvBusy] = useState(false);
  const [progress, setProgress] = useState<Progress[]>([]);
  const [deploying, setDeploying] = useState(false);
  const [result, setResult] = useState<DeployResult | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [logs, setLogs] = useState<string[]>([]);
  const [status, setStatus] = useState<ServerStatus | null>(null);

  const refreshEnv = useCallback(async () => {
    setEnvBusy(true);
    try {
      if (!isDesktop) {
        // Browser preview: let the user walk the wizard so the flow can be
        // reviewed, but mark every check as informational and block deploying.
        setEnv({
          os: navigator.platform || 'browser',
          arch: 'web',
          binary_found: false,
          binary_path: null,
          binary_version: null,
          data_dir: '（桌面客户端中自动选择）',
          data_dir_writable: false,
          api_port_free: true,
          control_port_free: true,
          docker_available: false,
          existing_deployment: false,
          ready: true,
          checks: [
            { id: 'preview', label: '运行环境', ok: false, fatal: false,
              detail: '浏览器预览模式：无法启动本地服务端，请使用桌面客户端或 docker compose' },
            { id: 'ports', label: '默认端口 8443 / 9443', ok: true, fatal: false, detail: '将在桌面客户端中实际检测' },
            { id: 'data', label: '数据目录', ok: true, fatal: false, detail: '桌面客户端默认使用系统应用数据目录' },
          ],
        });
        return;
      }
      const report = await desktop.envCheck({
        dataDir: form.dataDir || undefined,
        apiPort: Number(form.apiPort) || 8443,
        controlPort: Number(form.controlPort) || 9443,
      });
      setEnv(report);
      setForm((prev) => ({ ...prev, dataDir: prev.dataDir || report.data_dir }));
    } catch (e) {
      toast.error('环境检测失败', e instanceof Error ? e.message : '');
    } finally {
      setEnvBusy(false);
    }
  }, [form.apiPort, form.controlPort, form.dataDir, toast]);

  useEffect(() => {
    void refreshEnv();
    if (!isDesktop) return;
    void desktop.serverStatus().then(setStatus).catch(() => {});
    let unProgress: (() => void) | undefined;
    let unLog: (() => void) | undefined;
    void listen<Progress>('deploy://progress', (p) => {
      setProgress((prev) => {
        const idx = prev.findIndex((x) => x.step === p.step);
        if (idx >= 0) {
          const copy = [...prev];
          copy[idx] = p;
          return copy;
        }
        return [...prev, p];
      });
      if (p.status === 'error' && p.detail) setFailure(p.detail);
    }).then((fn) => (unProgress = fn));
    void listen<string>('deploy://log', (chunk) => {
      setLogs((prev) => [...prev.slice(-300), ...chunk.split('\n').filter(Boolean)]);
    }).then((fn) => (unLog = fn));
    return () => {
      unProgress?.();
      unLog?.();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const deploy = async () => {
    if (!isDesktop) {
      toast.error('需要桌面客户端', '浏览器预览无法启动本地服务端进程');
      return;
    }
    setDeploying(true);
    setProgress([]);
    setFailure(null);
    setResult(null);
    const req: DeployRequest = {
      data_dir: form.dataDir,
      api_port: Number(form.apiPort),
      control_port: Number(form.controlPort),
      public_host: form.publicHost,
      admin_user: form.adminUser,
      admin_password: form.adminPassword,
      domain_binding: form.domainBinding,
      domain_suffix: form.domainSuffix,
      port_min: Number(form.portMin),
      port_max: Number(form.portMax),
      tls_mode: form.tlsMode,
      tls_cert: form.tlsCert || undefined,
      tls_key: form.tlsKey || undefined,
    };
    try {
      const res = await desktop.deployServer(req);
      setResult(res);
      setStep(4);
      toast.success('服务端部署成功', res.webui_url);
      void desktop.serverStatus().then(setStatus).catch(() => {});
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setFailure(msg);
      toast.error('部署失败', msg);
    } finally {
      setDeploying(false);
    }
  };

  const canNext = () => {
    if (step === 0) return Boolean(env?.ready);
    if (step === 1) {
      const api = Number(form.apiPort);
      const ctl = Number(form.controlPort);
      return api > 0 && api < 65536 && ctl > 0 && ctl < 65536 && api !== ctl && Number(form.portMin) < Number(form.portMax);
    }
    if (step === 2) {
      const pwOk = form.adminPassword.length === 0 || form.adminPassword.length >= 8;
      const tlsOk = form.tlsMode === 'self-signed' || (Boolean(form.tlsCert) && Boolean(form.tlsKey));
      const domainOk = !form.domainBinding || form.domainSuffix.trim().length > 0;
      return Boolean(form.adminUser.trim()) && pwOk && tlsOk && domainOk;
    }
    return true;
  };

  return (
    <>
      <div className="page-head">
        <div>
          <button className="nav-item" style={{ width: 'auto', padding: 0, color: 'var(--text-dim)' }} onClick={() => navigate('/connect')}>
            <ChevronLeft size={15} /> 返回连接页
          </button>
          <h1 style={{ marginTop: 8 }}>一键部署服务端</h1>
          <p>在向导中完成配置，点击「开始部署」后会自动初始化数据库、生成 TLS 证书并启动服务端。</p>
        </div>
        {status?.running && (
          <Badge tone={status.healthy ? 'good' : 'warn'}>
            {status.healthy ? '服务端运行中' : '服务端已启动，健康检查未通过'}
          </Badge>
        )}
      </div>

      {!isDesktop && (
        <div className="alert alert-warn">
          <AlertTriangle size={18} />
          当前在浏览器中预览。一键部署需要桌面客户端（Tauri）来启动本地服务端进程。
        </div>
      )}

      <div className="wizard-steps">
        {STEPS.map((label, i) => (
          <div key={label} className={`wizard-step ${i === step ? 'active' : ''} ${i < step ? 'done' : ''}`}>
            <span className="wizard-index">{i < step ? <CheckCircle2 size={14} /> : i + 1}</span>
            <span>{label}</span>
          </div>
        ))}
      </div>

      {step === 0 && (
        <Card>
          <CardHeader
            title="环境检测"
            subtitle="检查内置服务端组件、数据目录权限与端口占用情况"
            actions={<Button size="sm" variant="subtle" onClick={refreshEnv} disabled={envBusy}><RefreshCw size={13} className={envBusy ? 'spinner-icon' : ''} /> 重新检测</Button>}
          />
          {envBusy && !env ? (
            <Spinner label="正在检测环境…" />
          ) : !env ? (
            <div className="alert alert-warn">
              <AlertTriangle size={18} />
              <div>
                无法完成环境检测。请确认已安装完整版桌面客户端（需要内置的服务端组件），
                或在服务器上使用 <span className="mono">docker compose up -d</span> 部署。
              </div>
            </div>
          ) : (
            <>
              <div className="check-list">
                {env.checks.map((c) => (
                  <div key={c.id} className={`check-item ${c.ok ? 'ok' : c.fatal ? 'bad' : 'warn'}`}>
                    {c.ok ? <CheckCircle2 size={16} /> : <AlertTriangle size={16} />}
                    <div>
                      <strong>{c.label}</strong>
                      <div className="small">{c.detail}</div>
                    </div>
                  </div>
                ))}
              </div>
              <div className="row gap-16 small faint wrap" style={{ marginTop: 12 }}>
                <span>系统：{env.os} / {env.arch}</span>
                <span>组件：{env.binary_version ?? '—'}</span>
                <span>Docker：{env.docker_available ? '可用' : '未安装'}</span>
              </div>
            </>
          )}
        </Card>
      )}

      {step === 1 && (
        <div className="grid grid-2">
          <Card>
            <CardHeader title="监听端口" subtitle="API 端口用于 WebUI 与客户端 API；控制端口用于隧道长连接" />
            <div className="grid grid-2">
              <Field label="API / WebUI 端口">
                <input value={form.apiPort} onChange={(e) => setForm({ ...form, apiPort: digits(e.target.value) })} />
              </Field>
              <Field label="隧道控制端口">
                <input value={form.controlPort} onChange={(e) => setForm({ ...form, controlPort: digits(e.target.value) })} />
              </Field>
            </div>
            <Field label="公网入口地址" hint="客户端拿到的 IP/域名；留空自动探测本机 IP">
              <input value={form.publicHost} onChange={(e) => setForm({ ...form, publicHost: e.target.value })} placeholder="203.0.113.10" />
            </Field>
          </Card>
          <Card>
            <CardHeader title="数据目录与端口范围" subtitle="公网端口只会从该范围内分配" />
            <Field label="数据目录" hint="数据库、TLS 证书与日志都保存在这里">
              <div className="row gap-8">
                <input value={form.dataDir} onChange={(e) => setForm({ ...form, dataDir: e.target.value })} />
                <Button
                  variant="subtle"
                  size="sm"
                  onClick={async () => {
                    const dir = await desktop.pickDirectory();
                    if (dir) setForm({ ...form, dataDir: dir });
                  }}
                >
                  <FolderOpen size={13} /> 选择
                </Button>
              </div>
            </Field>
            <div className="grid grid-2">
              <Field label="端口范围开始">
                <input value={form.portMin} onChange={(e) => setForm({ ...form, portMin: digits(e.target.value) })} />
              </Field>
              <Field label="端口范围结束">
                <input value={form.portMax} onChange={(e) => setForm({ ...form, portMax: digits(e.target.value) })} />
              </Field>
            </div>
            <div className="alert alert-info small">部署后请确保防火墙放行 {form.apiPort}/tcp、{form.controlPort}/tcp 与 {form.portMin}-{form.portMax} 端口段。</div>
          </Card>
        </div>
      )}

      {step === 2 && (
        <div className="grid grid-2">
          <Card>
            <CardHeader title="管理员账号" subtitle="用于首次登录 WebUI；密码留空将随机生成并显示一次" />
            <Field label="管理员用户名">
              <input value={form.adminUser} onChange={(e) => setForm({ ...form, adminUser: e.target.value })} />
            </Field>
            <Field label="管理员密码" hint="至少 8 位；留空自动生成">
              <input type="password" value={form.adminPassword} onChange={(e) => setForm({ ...form, adminPassword: e.target.value })} />
            </Field>
            <div className="row gap-12" style={{ marginTop: 8 }}>
              <Switch checked={form.domainBinding} onChange={(v) => setForm({ ...form, domainBinding: v })} label="域名绑定" />
              <div>
                <div style={{ fontWeight: 600 }}>开启域名绑定</div>
                <div className="faint small">开启后新建隧道返回「域名 + 端口」</div>
              </div>
            </div>
            <Field label="域名后缀" hint="例如 tunnel.example.com">
              <input
                value={form.domainSuffix}
                onChange={(e) => setForm({ ...form, domainSuffix: e.target.value })}
                placeholder="tunnel.example.com"
                disabled={!form.domainBinding}
              />
            </Field>
          </Card>
          <Card>
            <CardHeader title="TLS 配置" subtitle="必须启用；默认自动生成自签名证书" />
            <div className="segmented">
              <button type="button" className={form.tlsMode === 'self-signed' ? 'active' : ''} onClick={() => setForm({ ...form, tlsMode: 'self-signed' })}>
                自动生成（自签名）
              </button>
              <button type="button" className={form.tlsMode === 'existing' ? 'active' : ''} onClick={() => setForm({ ...form, tlsMode: 'existing' })}>
                使用已有证书
              </button>
            </div>
            {form.tlsMode === 'existing' ? (
              <>
                <Field label="证书文件（.crt / .pem）">
                  <div className="row gap-8">
                    <input value={form.tlsCert} onChange={(e) => setForm({ ...form, tlsCert: e.target.value })} />
                    <Button size="sm" variant="subtle" onClick={async () => {
                      const f = await desktop.pickFile([{ name: '证书', extensions: ['crt', 'pem', 'cer'] }]);
                      if (f) setForm((prev) => ({ ...prev, tlsCert: f }));
                    }}>选择</Button>
                  </div>
                </Field>
                <Field label="私钥文件（.key / .pem）">
                  <div className="row gap-8">
                    <input value={form.tlsKey} onChange={(e) => setForm({ ...form, tlsKey: e.target.value })} />
                    <Button size="sm" variant="subtle" onClick={async () => {
                      const f = await desktop.pickFile([{ name: '私钥', extensions: ['key', 'pem'] }]);
                      if (f) setForm((prev) => ({ ...prev, tlsKey: f }));
                    }}>选择</Button>
                  </div>
                </Field>
              </>
            ) : (
              <div className="alert alert-info small">
                <ShieldCheck size={16} />
                将生成有效期为 10 年的自签名证书，并在部署完成后显示 SHA-256 指纹，客户端可据此固定信任。
              </div>
            )}
          </Card>
        </div>
      )}

      {step === 3 && (
        <div className="grid grid-2">
          <Card>
            <CardHeader title="确认配置" subtitle="点击开始部署后将依次执行初始化与启动" />
            <dl className="kv">
              <dt>数据目录</dt><dd className="mono small">{form.dataDir}</dd>
              <dt>WebUI</dt><dd className="mono">https://本机:{form.apiPort}</dd>
              <dt>控制端口</dt><dd className="mono">{form.controlPort}</dd>
              <dt>公网入口</dt><dd className="mono">{form.publicHost || '自动探测'}</dd>
              <dt>端口范围</dt><dd className="mono">{form.portMin}-{form.portMax}</dd>
              <dt>TLS</dt><dd>{form.tlsMode === 'self-signed' ? '自签名（自动生成）' : '已有证书'}</dd>
              <dt>管理员</dt><dd className="mono">{form.adminUser}</dd>
              <dt>密码</dt><dd>{form.adminPassword ? '已设置' : '自动生成'}</dd>
              <dt>域名绑定</dt><dd>{form.domainBinding ? `开启（${form.domainSuffix}）` : '关闭'}</dd>
            </dl>
          </Card>
          <Card>
            <CardHeader title="部署进度" subtitle="实时输出每个步骤的结果" />
            <div className="progress-track"><span style={{ width: `${progress.at(-1)?.percent ?? 0}%` }} /></div>
            <div className="check-list" style={{ marginTop: 14 }}>
              {progress.length === 0 && <p className="faint small">等待开始…</p>}
              {progress.map((p) => (
                <div key={p.step} className={`check-item ${p.status === 'done' ? 'ok' : p.status === 'error' ? 'bad' : 'warn'}`}>
                  {p.status === 'done' ? <CheckCircle2 size={16} /> : p.status === 'error' ? <AlertTriangle size={16} /> : <Loader2 size={16} className="spinner-icon" />}
                  <div>
                    <strong>{p.message}</strong>
                    {p.detail && <div className="small mono" style={{ wordBreak: 'break-all' }}>{p.detail}</div>}
                  </div>
                </div>
              ))}
            </div>
            {failure && (
              <div className="alert alert-bad" style={{ marginTop: 12 }}>
                <div>
                  <strong>部署失败</strong>
                  <pre className="mono-block" style={{ marginTop: 8 }}>{failure}</pre>
                </div>
              </div>
            )}
            {logs.length > 0 && (
              <>
                <div className="faint small" style={{ marginTop: 12 }}><Terminal size={12} /> 服务端日志</div>
                <pre className="mono-block" style={{ maxHeight: 180 }}>{logs.join('\n')}</pre>
              </>
            )}
          </Card>
        </div>
      )}

      {step === 4 && result && (
        <Card>
          <CardHeader title="部署完成" subtitle="WebUI 已可访问，请使用下面的管理员账号登录" />
          <div className="check-list">
            <div className="check-item ok"><CheckCircle2 size={16} /><div><strong>服务端已启动</strong><div className="small">{result.webui_url}</div></div></div>
            <div className="check-item ok"><CheckCircle2 size={16} /><div><strong>健康检查通过</strong><div className="small">GET /api/v1/health → 200</div></div></div>
            <div className="check-item ok"><CheckCircle2 size={16} /><div><strong>数据目录</strong><div className="small mono">{result.data_dir}</div></div></div>
          </div>
          <div className="grid grid-2" style={{ marginTop: 16 }}>
            <div>
              <div className="faint small">WebUI 地址</div>
              <div className="row gap-8" style={{ marginTop: 6 }}>
                <span className="mono">{result.webui_url}</span>
                <Button size="sm" variant="ghost" onClick={() => { void copyText(result.webui_url); toast.success('已复制'); }}><Copy size={12} /></Button>
                <Button size="sm" variant="subtle" onClick={() => void desktop.openExternal(result.webui_url)}><ExternalLink size={12} /> 打开</Button>
              </div>
            </div>
            <div>
              <div className="faint small">管理员</div>
              <div className="mono" style={{ marginTop: 6 }}>{result.admin_user}</div>
              {result.generated_password && (
                <div className="row gap-8" style={{ marginTop: 4 }}>
                  <span className="mono small">{result.generated_password}</span>
                  <Button size="sm" variant="ghost" onClick={() => { void copyText(result.generated_password!); toast.success('已复制密码'); }}><Copy size={12} /></Button>
                </div>
              )}
            </div>
          </div>
          {result.fingerprint && (
            <div style={{ marginTop: 14 }}>
              <div className="faint small">TLS 证书指纹（SHA-256）</div>
              <div className="mono small" style={{ wordBreak: 'break-all' }}>{result.fingerprint}</div>
            </div>
          )}
          <div className="row gap-8" style={{ marginTop: 18 }}>
            <Button onClick={() => void desktop.openExternal(result.webui_url)}><ExternalLink size={15} /> 打开 WebUI</Button>
            <Button variant="subtle" onClick={() => navigate('/connect')}><Server size={15} /> 去登录</Button>
            <Button variant="subtle" onClick={() => { setStep(0); setResult(null); setProgress([]); void refreshEnv(); }}><RefreshCw size={15} /> 重新检测</Button>
            {status?.running && (
              <Button variant="danger" onClick={async () => {
                await desktop.stopServer();
                toast.info('服务端已停止');
                setStatus(await desktop.serverStatus());
              }}><Square size={15} /> 停止服务端</Button>
            )}
          </div>
        </Card>
      )}

      {step < 4 && (
        <div className="wizard-footer">
          <Button variant="ghost" onClick={() => setStep((s) => Math.max(0, s - 1) as Step)} disabled={step === 0 || deploying}>
            <ChevronLeft size={15} /> 上一步
          </Button>
          <span className="grow" />
          {step === 3 ? (
            <Button onClick={deploy} disabled={deploying || !isDesktop} title={!isDesktop ? '需要桌面客户端' : undefined}>
              {deploying ? <Loader2 size={15} className="spinner-icon" /> : <Rocket size={15} />}
              {deploying ? '部署中…' : '开始部署'}
            </Button>
          ) : (
            <Button onClick={() => setStep((s) => Math.min(3, s + 1) as Step)} disabled={!canNext()}>
              下一步 <ChevronRight size={15} />
            </Button>
          )}
        </div>
      )}
    </>
  );
}

function digits(v: string): string {
  return v.replace(/\D/g, '');
}

export function DeployHint() {
  return (
    <Card>
      <CardHeader title="部署方式" subtitle="两种方式得到的服务端完全一致" />
      <div className="hint-list">
        <div className="row gap-8"><Server size={15} /> 图形向导：本页，适合本机或带桌面的服务器</div>
        <div className="row gap-8"><Terminal size={15} /> 命令行：<span className="mono">docker compose up -d</span></div>
        <div className="row gap-8"><Play size={15} /> 远程管理：部署后在「连接」中填写服务端地址</div>
      </div>
    </Card>
  );
}
