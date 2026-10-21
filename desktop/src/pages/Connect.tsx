import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { KeyRound, Loader2, LockKeyhole, Server, ShieldCheck, Ticket } from 'lucide-react';
import { useAuth } from '@web/state/auth';
import { Button, Field } from '@web/components/ui';
import { ApiError } from '@web/lib/api';
import { desktop, isDesktop } from '../lib/tauri';
import { readInsecure, writeInsecure } from '../lib/transport';

/**
 * First-run experience: the only thing a user must know is the server address.
 * Credentials are provisioned by the administrator — there is no sign-up.
 */
export default function ConnectPage() {
  const { user, login, loginWithKey, loginWithToken, serverUrl, setServerUrl } = useAuth();
  const navigate = useNavigate();

  // Once the session is established (password, key, or token), go to the app.
  useEffect(() => {
    if (user) navigate('/', { replace: true });
  }, [user, navigate]);

  const [url, setUrl] = useState(serverUrl ?? 'https://');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [token, setToken] = useState('');
  const [mode, setMode] = useState<'password' | 'key' | 'token'>('password');
  const [keyPath, setKeyPath] = useState('');
  const [insecure, setInsecure] = useState(readInsecure);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const normalize = (raw: string) => {
    let s = raw.trim();
    if (!s) return '';
    if (!s.includes('://')) s = 'https://' + s;
    return s.replace(/\/+$/, '').replace(/\/api\/v1$/, '');
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    const server = normalize(url);
    if (!server) {
      setError('请填写服务端地址，例如 https://1.2.3.4:8443');
      return;
    }
    if (mode !== 'token' && !username.trim()) {
      setError('请填写用户名');
      return;
    }
    setBusy(true);
    try {
      writeInsecure(insecure);
      setServerUrl(server);
      // Give React a tick so the api base URL is applied before login.
      await new Promise((r) => setTimeout(r, 0));
      if (mode === 'password') {
        if (!password) throw new Error('请输入密码');
        await login(username.trim(), password);
      } else if (mode === 'key') {
        if (!keyPath) throw new Error('请选择 RSA 私钥文件');
        const pem = await readKeyFile(keyPath);
        await loginWithKey(username.trim(), pem);
      } else {
        if (!token.trim()) throw new Error('请输入 API Token 或管理员令牌');
        await loginWithToken(token.trim());
      }
    } catch (err) {
      if (err instanceof ApiError || err instanceof Error) setError(err.message);
      else setError('登录失败，请重试');
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
        <h1>连接你的穿透服务端</h1>
        <p>支持本地或远程服务端管理。使用账号密码或专属 Token 一键连接。</p>
        <ul>
          <li>支持管理 Token 远程管理（Docker 日志开箱即见）</li>
          <li>支持密码与 RSA / SSH 私钥安全认证</li>
          <li>节点延迟实时可见，一键创建隧道</li>
          <li>自动识别管理员权限，解锁远程管理面板</li>
        </ul>
      </div>
      <div className="auth-panel">
        <form className="auth-card" onSubmit={submit}>
          <div>
            <h2>连接与登录</h2>
            <p className="small">输入服务端地址；管理 Token 支持免密码直接登录。</p>
          </div>
          <Field label="服务端地址" hint="例如 https://1.2.3.4:8443 或 https://panel.example.com">
            <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" autoFocus />
          </Field>
          <div className="segmented">
            <button type="button" className={mode === 'password' ? 'active' : ''} onClick={() => setMode('password')}>
              <LockKeyhole size={14} style={{ marginRight: 6, verticalAlign: -2 }} />密码
            </button>
            <button type="button" className={mode === 'token' ? 'active' : ''} onClick={() => setMode('token')}>
              <Ticket size={14} style={{ marginRight: 6, verticalAlign: -2 }} />管理 Token
            </button>
            <button type="button" className={mode === 'key' ? 'active' : ''} onClick={() => setMode('key')}>
              <KeyRound size={14} style={{ marginRight: 6, verticalAlign: -2 }} />RSA Key
            </button>
          </div>
          {mode !== 'token' && (
            <Field label="用户名">
              <input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="alice" autoComplete="username" />
            </Field>
          )}
          {mode === 'password' && (
            <Field label="密码">
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
            </Field>
          )}
          {mode === 'token' && (
            <Field label="管理 / API 令牌" hint="粘贴 Docker 容器日志或服务端 admin-token.txt 输出的 yk_... 令牌">
              <input
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="yk_..."
                autoComplete="off"
              />
            </Field>
          )}
          {mode === 'key' && (
            <Field label="私钥文件" hint="支持 OpenSSH / PEM 格式；也可直接粘贴到下方">
              <div className="row gap-8">
                <input value={keyPath} onChange={(e) => setKeyPath(e.target.value)} placeholder="~/.ssh/id_rsa" />
                {isDesktop && <Button type="button" variant="subtle" size="sm" onClick={pickKey}>选择</Button>}
              </div>
            </Field>
          )}
          <label className="checkbox-row">
            <input type="checkbox" checked={insecure} onChange={(e) => setInsecure(e.target.checked)} />
            信任自签名证书（服务端使用默认证书时勾选）
          </label>
          {error && <div className="alert alert-bad">{error}</div>}
          <Button type="submit" disabled={busy}>
            {busy ? <Loader2 size={16} className="spinner-icon" /> : <ShieldCheck size={16} />}
            {busy ? '正在连接…' : mode === 'token' ? '以管理身份连接' : '连接并登录'}
          </Button>
          <div className="hint-list">
            <span className="row gap-8"><Server size={14} /> 远程管理？在服务端运行 <code className="mono">docker logs yuki-server</code> 即可获取 Token。</span>
            {!isDesktop && <span className="row gap-8 faint">当前在浏览器中预览，隧道转发需要桌面客户端。</span>}
          </div>
        </form>
      </div>
    </div>
  );
}

async function readKeyFile(path: string): Promise<string> {
  if (!isDesktop) throw new Error('请在桌面客户端中选择私钥文件');
  return desktop.readPrivateKey(path);
}
