import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { KeyRound, Loader2, LockKeyhole, Server, ShieldCheck } from 'lucide-react';
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
  const { user, login, loginWithKey, serverUrl, setServerUrl } = useAuth();
  const navigate = useNavigate();

  // Once the session is established (password or key), go to the tunnel list.
  useEffect(() => {
    if (user) navigate('/', { replace: true });
  }, [user, navigate]);

  const [url, setUrl] = useState(serverUrl ?? 'https://');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [mode, setMode] = useState<'password' | 'key'>('password');
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
    if (!username.trim()) {
      setError('请填写管理员分配的用户名');
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
      } else {
        if (!keyPath) throw new Error('请选择 RSA 私钥文件');
        const pem = await readKeyFile(keyPath);
        await loginWithKey(username.trim(), pem);
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
        <p>只需要服务端地址，以及管理员为你分配的账号。无需编辑任何配置文件。</p>
        <ul>
          <li>支持密码与 RSA / SSH 私钥登录</li>
          <li>节点延迟实时可见，一键创建隧道</li>
          <li>断线自动重连，托盘常驻后台</li>
        </ul>
      </div>
      <div className="auth-panel">
        <form className="auth-card" onSubmit={submit}>
          <div>
            <h2>登录</h2>
            <p className="small">首次使用请填写服务端地址；之后会自动记住。</p>
          </div>
          <Field label="服务端地址" hint="例如 https://1.2.3.4:8443 或 https://panel.example.com">
            <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://" autoFocus />
          </Field>
          <Field label="用户名">
            <input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="alice" autoComplete="username" />
          </Field>
          <div className="segmented">
            <button type="button" className={mode === 'password' ? 'active' : ''} onClick={() => setMode('password')}>
              <LockKeyhole size={14} style={{ marginRight: 6, verticalAlign: -2 }} />密码
            </button>
            <button type="button" className={mode === 'key' ? 'active' : ''} onClick={() => setMode('key')}>
              <KeyRound size={14} style={{ marginRight: 6, verticalAlign: -2 }} />RSA Key
            </button>
          </div>
          {mode === 'password' ? (
            <Field label="密码">
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
            </Field>
          ) : (
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
            {busy ? '正在连接…' : '连接并登录'}
          </Button>
          <div className="hint-list">
            <span className="row gap-8"><Server size={14} /> 不知道地址？请联系服务端管理员。</span>
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
