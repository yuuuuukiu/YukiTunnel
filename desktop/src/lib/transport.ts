import { setTransport } from '@web/lib/api';
import { isDesktop } from './tauri';

/**
 * Routes API calls through Rust (reqwest) instead of the webview's fetch.
 *
 * Why: a self-hosted YukiTunnel usually starts with a self-signed certificate,
 * and webviews refuse those. Rust can accept the certificate only when the user
 * explicitly opted in, and it also sidesteps CORS for `tauri://localhost`.
 */
export function installDesktopTransport(getInsecure: () => boolean) {
  if (!isDesktop) return;
  setTransport(async ({ method, url, token, body }) => {
    const { invoke } = await import('@tauri-apps/api/core');
    return invoke<{ status: number; body: string }>('api_request', {
      req: {
        method,
        url,
        token: token ?? null,
        body: body ?? null,
        insecure: getInsecure(),
      },
    });
  });
}

const INSECURE_KEY = 'yukitunnel.insecure';

export function readInsecure(): boolean {
  return localStorage.getItem(INSECURE_KEY) === '1';
}

export function writeInsecure(v: boolean) {
  if (v) localStorage.setItem(INSECURE_KEY, '1');
  else localStorage.removeItem(INSECURE_KEY);
}
