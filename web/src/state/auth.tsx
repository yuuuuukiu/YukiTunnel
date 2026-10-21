import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, ApiError, configureApi, currentToken, setUnauthorizedHandler } from '../lib/api';
import type { User } from '../lib/types';

interface AuthState {
  user: User | null;
  token: string | null;
  loading: boolean;
  /** Base server URL for the desktop build; null means same origin. */
  serverUrl: string | null;
  setServerUrl: (url: string | null) => void;
  login: (username: string, password: string, remember?: boolean) => Promise<void>;
  loginWithKey: (username: string, privateKeyPem: string, remember?: boolean) => Promise<void>;
  loginWithToken: (token: string, remember?: boolean) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
  /** remembered holds the fields the last sign-in saved for prefill. */
  remembered: RememberedForm;
  /** forgetCredentials drops the saved prefill without ending the session. */
  forgetCredentials: () => void;
}

/** RememberedForm is the non-secret part of a saved sign-in, used to prefill. */
export interface RememberedForm {
  serverUrl?: string;
  username?: string;
  mode?: 'password' | 'token' | 'key';
  /** True when a management token was stored, so the form can show a hint. */
  hasToken?: boolean;
  /** True when a password was stored (desktop only; see rememberPassword). */
  hasPassword?: boolean;
}

const AuthContext = createContext<AuthState | null>(null);
const STORAGE_KEY = 'yukitunnel.session';
const REMEMBER_KEY = 'yukitunnel.remembered';

/** apiBase turns a user-entered server URL into the API mount point. */
export function apiBase(serverUrl: string | null): string {
  if (!serverUrl) return '/api/v1';
  const trimmed = serverUrl.trim().replace(/\/+$/, '').replace(/\/api\/v1$/, '');
  return trimmed + '/api/v1';
}

interface StoredSession {
  token: string;
  serverUrl?: string;
  user?: User;
}

/**
 * SavedSignIn is what "记住凭据" persists.
 *
 * The session token already keeps a user signed in across restarts, but it
 * expires and is invalidated on logout, and it says nothing about which
 * username or server to use. This record covers the case where the token is
 * gone: the form comes back pre-filled instead of blank.
 *
 * Secrets are only stored when the user explicitly ticks the box. A management
 * token is high privilege, so the caller must opt in every time; the password
 * is never written by the web build (it would live in localStorage with no
 * protection) and only by the desktop shell, where it can be kept in the OS
 * keychain later.
 */
interface SavedSignIn {
  serverUrl?: string;
  username?: string;
  mode?: 'password' | 'token' | 'key';
  token?: string;
  password?: string;
}

function readRemembered(): SavedSignIn | null {
  try {
    const raw = localStorage.getItem(REMEMBER_KEY);
    return raw ? (JSON.parse(raw) as SavedSignIn) : null;
  } catch {
    return null;
  }
}

/**
 * canStoreSecrets reports whether the host can protect a stored secret.
 *
 * In the browser, localStorage is readable by any script on the origin, so
 * persisting a password there would be a downgrade. The desktop shell runs a
 * single trusted bundle and can later move secrets to the OS keychain, so it
 * is allowed to keep them.
 */
function canStoreSecrets(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window;
}

function writeRemembered(next: SavedSignIn | null) {
  try {
    if (next && Object.keys(next).length > 0) {
      localStorage.setItem(REMEMBER_KEY, JSON.stringify(next));
    } else {
      localStorage.removeItem(REMEMBER_KEY);
    }
  } catch {
    /* storage disabled or full; signing in still works, just not remembered */
  }
}

function readSession(): StoredSession | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? (JSON.parse(raw) as StoredSession) : null;
  } catch {
    return null;
  }
}

/** parsePrivateKey accepts PEM RSA keys (PKCS#1 and PKCS#8). */
async function signNonce(privateKeyPem: string, nonce: string): Promise<string> {
  const pem = privateKeyPem.trim();
  const isPkcs1 = pem.includes('BEGIN RSA PRIVATE KEY');
  const isPkcs8 = pem.includes('BEGIN PRIVATE KEY');
  if (!isPkcs1 && !isPkcs8) {
    throw new Error('请粘贴 PEM 格式的 RSA 私钥（-----BEGIN RSA PRIVATE KEY----- 或 -----BEGIN PRIVATE KEY-----）');
  }
  const b64 = pem
    .replace(/-----[^-]+-----/g, '')
    .replace(/\s+/g, '');
  const binary = atob(b64);
  const der = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) der[i] = binary.charCodeAt(i);
  let key: CryptoKey;
  try {
    key = await crypto.subtle.importKey(
      isPkcs1 ? 'pkcs8' : 'pkcs8',
      (isPkcs1 ? pkcs1ToPkcs8(der) : der) as BufferSource,
      { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' },
      false,
      ['sign'],
    );
  } catch {
    throw new Error('无法解析私钥。浏览器仅支持未加密的 RSA 私钥；也可改用密码登录或桌面客户端。');
  }
  const data = new TextEncoder().encode('yukitunnel-key-auth:' + nonce);
  const sig = await crypto.subtle.sign('RSASSA-PKCS1-v1_5', key, data);
  return btoa(String.fromCharCode(...new Uint8Array(sig)));
}

/** pkcs1ToPkcs8 wraps a PKCS#1 RSAPrivateKey in a PKCS#8 PrivateKeyInfo. */
function pkcs1ToPkcs8(pkcs1: Uint8Array): Uint8Array<ArrayBuffer> {
  const rsaOid = [0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x01];
  const version = [0x02, 0x01, 0x00];
  const algorithm = [0x30, 0x0d, ...rsaOid, 0x05, 0x00];
  const octet = [0x04, ...derLen(pkcs1.length)];
  const innerLen = version.length + algorithm.length + octet.length + pkcs1.length;
  const header = [0x30, ...derLen(innerLen), ...version, ...algorithm, ...octet];
  const out = new Uint8Array(new ArrayBuffer(header.length + pkcs1.length));
  out.set(header, 0);
  out.set(pkcs1, header.length);
  return out;
}

function derLen(n: number): number[] {
  if (n < 0x80) return [n];
  const bytes: number[] = [];
  let v = n;
  while (v > 0) {
    bytes.unshift(v & 0xff);
    v >>= 8;
  }
  return [0x80 | bytes.length, ...bytes];
}

export function AuthProvider({ children, initialServerUrl = null }: { children: ReactNode; initialServerUrl?: string | null }) {
  const [user, setUser] = useState<User | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [serverUrl, setServerUrlState] = useState<string | null>(initialServerUrl);
  const [remembered, setRemembered] = useState<RememberedForm>(() => {
    const saved = readRemembered();
    if (!saved) return {};
    return {
      serverUrl: saved.serverUrl,
      username: saved.username,
      mode: saved.mode,
      hasToken: Boolean(saved.token),
      hasPassword: Boolean(saved.password),
    };
  });
  // A ref keeps the latest value available to callbacks without re-creating
  // them (and therefore without re-running the session restore effect).
  const serverUrlRef = useRef<string | null>(initialServerUrl);

  const setServerUrl = useCallback((url: string | null) => {
    serverUrlRef.current = url;
    setServerUrlState(url);
    configureApi({ baseUrl: apiBase(url) });
  }, []);

  const persist = useCallback((next: StoredSession | null) => {
    if (next) localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
    else localStorage.removeItem(STORAGE_KEY);
  }, []);

  const applySession = useCallback(
    (res: { token: string; user: User }) => {
      configureApi({ token: res.token });
      setToken(res.token);
      setUser(res.user);
      persist({ token: res.token, serverUrl: serverUrlRef.current ?? undefined, user: res.user });
    },
    [persist],
  );

  const clear = useCallback(() => {
    configureApi({ token: null });
    setToken(null);
    setUser(null);
    persist(null);
  }, [persist]);



  /** saveRemembered records (or clears) the prefill for the next visit. */
  const saveRemembered = useCallback((next: SavedSignIn | null) => {
    writeRemembered(next);
    if (!next) {
      setRemembered({});
      return;
    }
    setRemembered({
      serverUrl: next.serverUrl,
      username: next.username,
      mode: next.mode,
      hasToken: Boolean(next.token),
      hasPassword: Boolean(next.password),
    });
  }, []);

  /**
   * autoSignIn retries the last sign-in with saved credentials.
   *
   * A session token expires (7 days by default) and is cleared on logout, but
   * "记住此设备" is a promise that the app comes back ready to use. Only
   * secrets the user opted to store are used here; a failure falls through to
   * the normal login form without surfacing an error.
   */
  const autoSignIn = useCallback(async (url: string | null) => {
    const saved = readRemembered();
    if (!saved) return;
    if (url) {
      serverUrlRef.current = url;
      setServerUrlState(url);
      configureApi({ baseUrl: apiBase(url) });
    }
    try {
      if (saved.token) {
        configureApi({ token: saved.token });
        const res = await api.me();
        applySession({ token: saved.token, user: res.user });
        return;
      }
      if (saved.username && saved.password) {
        const res = await api.login(saved.username, saved.password);
        applySession(res);
      }
    } catch {
      // Saved credentials are stale; fall back to the form.
      configureApi({ token: null });
    }
  }, [applySession]);

  useEffect(() => {
    setUnauthorizedHandler(() => clear());
    const saved = readSession();
    const url = saved?.serverUrl ?? initialServerUrl ?? null;
    if (url) {
      serverUrlRef.current = url;
      setServerUrlState(url);
      configureApi({ baseUrl: apiBase(url) });
    }
    if (!saved?.token) {
      // No live session. If credentials were saved, sign in again silently so
      // a restart does not interrupt the user with a login form.
      void autoSignIn(url).finally(() => setLoading(false));
      return;
    }
    configureApi({ token: saved.token });
    setToken(saved.token);
    api
      .me()
      .then((res) => {
        setUser(res.user);
        persist({ token: saved.token, serverUrl: url ?? undefined, user: res.user });
      })
      .catch(() => clear())
      .finally(() => setLoading(false));
    // autoSignIn is stable (useCallback with [applySession]); listing it keeps
    // the dependency honest without re-running the effect on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clear, persist, autoSignIn]);

  const login = useCallback(
    async (username: string, password: string, remember = false) => {
      const res = await api.login(username, password);
      applySession(res);
      saveRemembered({
        serverUrl: serverUrlRef.current ?? undefined,
        username,
        mode: 'password',
        // The web build cannot protect a stored password, so only the desktop
        // shell (which can move it to the keychain) is allowed to keep it.
        password: remember && canStoreSecrets() ? password : undefined,
      });
    },
    [applySession, saveRemembered],
  );

  const loginWithKey = useCallback(
    async (username: string, privateKeyPem: string, remember = false) => {
      const challenge = await api.keyChallenge(username);
      const signature = await signNonce(privateKeyPem, challenge.nonce);
      const res = await api.keyLogin(username, challenge.nonce, signature);
      applySession(res);
      // Never persist a private key; only remember who signed in.
      saveRemembered({
        serverUrl: serverUrlRef.current ?? undefined,
        username,
        mode: remember ? 'key' : undefined,
      });
    },
    [applySession, saveRemembered],
  );

  const loginWithToken = useCallback(
    async (rawToken: string, remember = false) => {
      const cleanToken = rawToken.trim();
      if (!cleanToken) throw new Error('请输入 API Token');
      configureApi({ token: cleanToken });
      try {
        const res = await api.me();
        applySession({ token: cleanToken, user: res.user });
        saveRemembered({
          serverUrl: serverUrlRef.current ?? undefined,
          mode: 'token',
          token: remember ? cleanToken : undefined,
        });
      } catch (err) {
        configureApi({ token: null });
        throw err;
      }
    },
    [applySession, saveRemembered],
  );

  const forgetCredentials = useCallback(() => {
    saveRemembered(null);
  }, [saveRemembered]);

  const logout = useCallback(async () => {
    try {
      if (currentToken()) await api.logout();
    } catch {
      /* ignore network errors on logout */
    }
    clear();
  }, [clear]);

  const refresh = useCallback(async () => {
    const res = await api.me();
    setUser(res.user);
    const saved = readSession();
    if (saved) persist({ ...saved, user: res.user });
  }, [persist]);

  const value = useMemo(
    () => ({
      user, token, loading, serverUrl, setServerUrl,
      login, loginWithKey, loginWithToken, logout, refresh,
      remembered, forgetCredentials,
    }),
    [
      user, token, loading, serverUrl, setServerUrl,
      login, loginWithKey, loginWithToken, logout, refresh,
      remembered, forgetCredentials,
    ],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider');
  return ctx;
}

export { ApiError };
