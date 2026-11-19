export interface AppConfig {
  deviceName: string;
  port: number;
  saveDir: string;
  token: string;
  confirmReceive: boolean;
  autoCopyText: boolean;
  clipboardAutoPush: boolean;
  selectedInterface: string;
  notifyOnReceive: boolean;
  openWindowOnStart: boolean;
}

export interface AdapterInfo {
  name: string;
  ip: string;
  kind: 'wifi' | 'hotspot' | 'ethernet' | 'virtual' | 'other';
  isVirtual: boolean;
  priority: number;
  recommended: boolean;
}

export interface ClientInfo {
  id: string;
  role: 'pc' | 'remote';
  deviceName: string;
  platform: 'ios' | 'ipad' | 'macos' | 'android' | 'windows' | 'web';
  ip: string;
  connectedAt: string;
}

export interface TransferItem {
  id: string;
  direction: 'in' | 'out';
  kind: 'file' | 'image' | 'text';
  name: string;
  size: number;
  mimeType: string;
  localPath?: string;
  textContent?: string;
  deviceName: string;
  status: string;
  createdAt: string;
}

export interface ClipboardItem {
  id: string;
  kind: 'text' | 'image';
  text?: string;
  imagePath?: string;
  mimeType?: string;
  source: 'pc' | 'remote';
  deviceName: string;
  createdAt: string;
}

export interface PendingRequest {
  id: string;
  deviceName: string;
  remoteIp: string;
  files: { name: string; size: number; type: string }[];
  totalSize: number;
  createdAt: string;
}

export interface ShortcutLog {
  id: string;
  method: string;
  path: string;
  remoteIp: string;
  status: number;
  message: string;
  createdAt: string;
}

export interface LocalBootstrap {
  config: AppConfig;
  port: number;
  activeAdapter: AdapterInfo;
  adapters: AdapterInfo[];
  baseUrl: string;
  pairUrl: string;
  mdnsHost: string;
  mdnsPairUrl: string;
  autostart: boolean;
  sendTo: boolean;
  firewallOk: boolean;
  devices: ClientInfo[];
  transfers: TransferItem[];
  clipboard: ClipboardItem[];
  pending: PendingRequest[];
  shortcutLogs: ShortcutLog[];
}

export interface RemoteBootstrap {
  pcName: string;
  confirmReceive: boolean;
  transfers: TransferItem[];
  clipboard: ClipboardItem[];
}

export interface PingResp {
  ok: boolean;
  app: string;
  deviceName: string;
  isLocal: boolean;
  authorized: boolean;
}

const TOKEN_STORAGE_KEY = 'airdropwin_token';
const DEVICE_NAME_KEY = 'airdropwin_device_name';

export function getStoredToken(): string {
  try {
    return localStorage.getItem(TOKEN_STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

export function setStoredToken(token: string) {
  try {
    if (token) localStorage.setItem(TOKEN_STORAGE_KEY, token);
    else localStorage.removeItem(TOKEN_STORAGE_KEY);
  } catch {
    // ignore storage quota errors
  }
}

export function getStoredDeviceName(): string {
  try {
    return localStorage.getItem(DEVICE_NAME_KEY) || '';
  } catch {
    return '';
  }
}

export function setStoredDeviceName(name: string) {
  try {
    localStorage.setItem(DEVICE_NAME_KEY, name);
  } catch {
    // ignore
  }
}

export function authHeaders(extra?: Record<string, string>): Record<string, string> {
  const h: Record<string, string> = { ...(extra || {}) };
  const tok = getStoredToken();
  if (tok) {
    h['X-Token'] = tok;
  }
  return h;
}

export async function apiJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    credentials: 'include',
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...authHeaders(),
      ...(init?.headers as Record<string, string> | undefined),
    },
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || `请求失败 (${res.status})`);
  }
  return data as T;
}

export function fileDownloadUrl(id: string, inline = false): string {
  const tok = getStoredToken();
  const params = new URLSearchParams();
  if (inline) params.set('inline', '1');
  if (tok) params.set('token', tok);
  const qs = params.toString();
  return `/api/files/${encodeURIComponent(id)}${qs ? '?' + qs : ''}`;
}

export function clipImageUrl(id: string): string {
  const tok = getStoredToken();
  return `/api/clipboard/${encodeURIComponent(id)}/image${tok ? `?token=${encodeURIComponent(tok)}` : ''}`;
}

export function wsUrl(role: 'pc' | 'remote', deviceName?: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const params = new URLSearchParams();
  params.set('role', role);
  if (deviceName) params.set('name', deviceName);
  const tok = getStoredToken();
  if (tok && role === 'remote') params.set('token', tok);
  return `${proto}//${window.location.host}/ws?${params.toString()}`;
}

export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let idx = 0;
  let val = bytes;
  while (val >= 1024 && idx < units.length - 1) {
    val /= 1024;
    idx++;
  }
  return `${val < 10 && idx > 0 ? val.toFixed(2) : val < 100 && idx > 0 ? val.toFixed(1) : Math.round(val)} ${units[idx]}`;
}

export function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    const now = new Date();
    const sameDay =
      d.getFullYear() === now.getFullYear() &&
      d.getMonth() === now.getMonth() &&
      d.getDate() === now.getDate();
    const hh = String(d.getHours()).padStart(2, '0');
    const mm = String(d.getMinutes()).padStart(2, '0');
    if (sameDay) return `${hh}:${mm}`;
    return `${d.getMonth() + 1}/${d.getDate()} ${hh}:${mm}`;
  } catch {
    return '';
  }
}

/**
 * Reliable cross-platform clipboard copy that works on iOS Safari over plain HTTP LAN
 * as well as secure HTTPS / localhost contexts.
 */
export async function copyTextSafe(text: string): Promise<boolean> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // fall through to textarea fallback
    }
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.top = '-9999px';
    ta.style.left = '-9999px';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, text.length);
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
