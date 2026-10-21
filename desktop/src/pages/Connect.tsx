import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  ArrowRight, KeyRound, Loader2, LockKeyhole, Monitor, ServerCog, ShieldCheck, Ticket,
} from 'lucide-react';
import { useAuth } from '@web/state/auth';
import { Button, Field } from '@web/components/ui';
import { ApiError } from '@web/lib/api';
import { desktop, isDesktop } from '../lib/tauri';
import { readInsecure, writeInsecure } from '../lib/transport';

/**
 * Entry point for both jobs this app does.
 *
 * The client connection and the server admin console are different tasks with
 * different credentials, so the user picks one first and then sees a form built
 * only for that choice. A single form with three tabs made admins hunt for the
 * token field among unrelated options.
 */
export default function ConnectPage() {
  const { user, serverUrl, setServerUrl, login, loginWithKey, loginWithToken } = useAuth();
  const navigate = useNavigate();

  useEffect(() => {
    if (user) navigate('/', { replace: true });
  }, [user, navigate]);

  const [role, setRole] = useState<'client' | 'admin'>('client');
  const [url, setUrl] = useState(serverUrl ?? 'https://');
  const [insecure, setInsecure] = useState(readInsecure);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  // client credentials
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [keyPath, setKeyPath] = useState('');
  const [credMode, setCredMode] = useState<'password' | 'key'>('password');

  // admin credentials
  const [token, setToken] = useState('');

  const normalize = (raw: string) => {
    let s = raw.trim();
    if (!s) return '';
    if (!s.includes('://')) s = 'https://' + s;
    return s.replace(/\/+$/, '').replace(/\/api\/v1$/, '');
  };

  const prepare = () => {
    const server = normalize(url);
    if (!server) throw new Error('请填写服务端地址，例如 https://1.2.3.4:8443');
    writeInsecure(insecure);
    setServerUrl(server);
    return server;
  };

  const submitClient = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      prepare();
      await new Promise((r) => setTimeout(r, 0));
      if (!username.trim()) throw new Error('请填写管理员分配的用户名');
      if (credMode === 'password') {
        if (!password) throw new Error('请输入密码');
        await login(username.trim(), password);
      } else {
        if (!keyPath) throw new Error('请选择 RSA 私钥文件');
        await loginWithKey(username.trim(), await readKeyFile(keyPath));
      }
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? err.message : '登录失败，请重试');
    } finally {
      setBusy(false);
    }
  };

  const submitAdmin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      prepare();
      await new Promise((r) => setTimeout(r, 0));
      if (!token.trim()) throw new Error('请粘贴管理 Token');
      await loginWithToken(token.trim());
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? err.message : '登录失败，请重试');
    } finally {
      setBusy(false);
    }
  };

  const pickKey = async () => {
    const path = await desktop.pickFile([
      { name: 'RSA 私钥', extensions: ['pem', 'key', 'rsa', 'ppk', ''] },
      { name: '所有文件', extensions: ['*'] },
    ]);
    if (path) setKeyPath(path);
  };

  const switchRole = (next: 'client' | 'admin') => {
    setRole(next);
    setError('');
  };

  return (
    <div className="auth-page">
      <div className="auth-hero">
        <div className="brand">
          <div className="brand-mark">Y</div>
          <div className="brand-text">
            <strong>YukiTunnel</strong>
            <span>桌面客户端</span>
          </div>
        </div>
        <h1>
          连接隧道
          <br />
          或管理服务端
        </h1>
        <p>两种身份，两套入口。客户端跑隧道，管理端改配置，互不干扰。</p>
        <ul>
          <li><strong>客户端</strong>：登录后管理自己的隧道，转发本机端口</li>
          <li><strong>管理端</strong>：用管理 Token 免密进入，管理用户 / 节点 / 云 Profile</li>
          <li>密码、RSA 私钥、管理 Token 三种凭据都走 TLS</li>
          <li>关闭窗口后驻留托盘，隧道继续运行</li>
        </ul>
      </div>

      <div className="auth-panel">
        <div className="auth-card">
          <div className="mode-switch">
            <button
              type="button"
              className={`mode-card ${role === 'client' ? 'active' : ''}`}
              onClick={() => switchRole('client')}
            >
              <span className="mode-title"><Monitor size={16} /> 客户端</span>
              <span className="mode-desc">跑隧道，转发本机端口</span>
            </button>
            <button
              type="button"
              className={`mode-card ${role === 'admin' ? 'active' : ''}`}
              onClick={() => switchRole('admin')}
            >
              <span className="mode-title">
                <ServerCog size={16} /> 服务端管理
                <span className="mode-badge">管理员</span>
              </span>
              <span className="mode-desc">用管理 Token 管理服务端</span>
            </button>
          </div>

          {role === 'client' ? (
            <form onSubmit={submitClient} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div>
                <h2>连接服务端</h2>
                <p className="auth-sub">填写管理员给你的地址和账号。首次连接后可勾选信任自签名证书。</p>
              </div>
              <Field label="服务端地址" hint="例如 https://1.2.3.4:8443">
                <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" autoFocus />
              </Field>
              <Field label="用户名">
                <input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="alice"
                  autoComplete="username"
                />
              </Field>
              <div className="segmented">
                <button type="button" className={credMode === 'password' ? 'active' : ''} onClick={() => setCredMode('password')}>
                  <LockKeyhole size={14} style={{ marginRight: 6, verticalAlign: -2 }} />密码
                </button>
                <button type="button" className={credMode === 'key' ? 'active' : ''} onClick={() => setCredMode('key')}>
                  <KeyRound size={14} style={{ marginRight: 6, verticalAlign: -2 }} />RSA Key
                </button>
              </div>
              {credMode === 'password' ? (
                <Field label="密码">
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoComplete="current-password"
                  />
                </Field>
              ) : (
                <Field label="私钥文件" hint="支持 OpenSSH / PEM 格式">
                  <div className="row gap-8">
                    <input value={keyPath} onChange={(e) => setKeyPath(e.target.value)} placeholder="~/.ssh/id_rsa" />
                    {isDesktop && (
                      <Button type="button" variant="subtle" size="sm" onClick={pickKey}>选择</Button>
                    )}
                  </div>
                </Field>
              )}
              <label className="checkbox-row">
                <input type="checkbox" checked={insecure} onChange={(e) => setInsecure(e.target.checked)} />
                信任自签名证书
              </label>
              {error && <div className="alert alert-bad">{error}</div>}
              <Button type="submit" disabled={busy}>
                {busy ? <Loader2 size={16} className="spinner-icon" /> : <ArrowRight size={16} />}
                {busy ? '正在连接…' : '连接并登录'}
              </Button>
            </form>
          ) : (
            <form onSubmit={submitAdmin} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
              <div>
                <h2>服务端管理</h2>
                <p className="auth-sub">
                  粘贴管理 Token 即可免密进入。Token 在服务端启动日志或
                  <code className="mono"> admin-token.txt </code>里。
                </p>
              </div>
              <Field label="服务端地址" hint="本地 Docker 通常是 https://127.0.0.1:8443">
                <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" autoFocus />
              </Field>
              <Field label="管理 Token" hint="形如 yk_... ，仅在服务端生成时显示一次">
                <input
                  type="password"
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  placeholder="yk_..."
                  autoComplete="off"
                />
              </Field>
              <label className="checkbox-row">
                <input type="checkbox" checked={insecure} onChange={(e) => setInsecure(e.target.checked)} />
                信任自签名证书
              </label>
              {error && <div className="alert alert-bad">{error}</div>}
              <Button type="submit" disabled={busy}>
                {busy ? <Loader2 size={16} className="spinner-icon" /> : <ShieldCheck size={16} />}
                {busy ? '正在验证…' : '进入管理端'}
              </Button>
              <div className="hint-list">
                <span className="row gap-8">
                  <Ticket size={14} /> 没有 Token？在服务端执行 <code className="mono">docker logs yuki-server</code> 获取。
                </span>
              </div>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}

async function readKeyFile(path: string): Promise<string> {
  if (!isDesktop) throw new Error('请在桌面客户端中选择私钥文件');
  return desktop.readPrivateKey(path);
}
