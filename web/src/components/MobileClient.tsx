import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  ArrowDownToLine,
  Check,
  Clipboard,
  Copy,
  Image as ImageIcon,
  Moon,
  Pencil,
  Plus,
  Radio,
  RefreshCw,
  Send,
  Sun,
  Upload,
  Volume2,
  VolumeX,
  X,
} from 'lucide-react';
import {
  apiJSON,
  authHeaders,
  clipImageUrl,
  ClipboardItem,
  copyTextSafe,
  fileDownloadUrl,
  formatBytes,
  formatTime,
  getStoredDeviceName,
  RemoteBootstrap,
  setStoredDeviceName,
  TransferItem,
  wsUrl,
} from '../api';
import { playChime, useSoundPref } from '../utils/sound';
import { FileExtStamp, isPreviewableImg, safePreviewText } from './PCDashboard';
import QuickLookModal from './QuickLookModal';

interface Props {
  pcName: string;
  isDark: boolean;
  onToggleTheme: () => void;
  onTokenInvalid: () => void;
}

function guessInitialDeviceName(): string {
  const saved = getStoredDeviceName();
  if (saved) return saved;
  const ua = navigator.userAgent;
  if (/iPhone/i.test(ua)) return '我的 iPhone';
  if (/iPad/i.test(ua)) return '我的 iPad';
  if (/Macintosh|Mac OS X/i.test(ua)) return '我的 Mac';
  if (/Android/i.test(ua)) return '我的手机';
  return '局域网设备';
}

export default function MobileClient({ pcName, isDark, onToggleTheme, onTokenInvalid }: Props) {
  const [tab, setTab] = useState<'files' | 'clipboard'>('files');
  const [boot, setBoot] = useState<RemoteBootstrap | null>(null);
  const [deviceName, setDeviceName] = useState(guessInitialDeviceName);
  const [editingName, setEditingName] = useState(false);
  const [wsOnline, setWsOnline] = useState(false);

  const [uploadState, setUploadState] = useState<{
    phase: 'idle' | 'waiting' | 'uploading';
    percent: number;
    loaded: number;
    total: number;
    speedBps: number;
    fileCount: number;
  }>({ phase: 'idle', percent: 0, loaded: 0, total: 0, speedBps: 0, fileCount: 0 });
  const xhrRef = useRef<XMLHttpRequest | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const clipImgInputRef = useRef<HTMLInputElement | null>(null);

  const [clipText, setClipText] = useState('');
  const [sendingClip, setSendingClip] = useState(false);
  const [livePCClip, setLivePCClip] = useState<string>('');
  const [dragging, setDragging] = useState(false);
  const [toast, setToast] = useState('');
  const [quickLook, setQuickLook] = useState<{ src: string; title: string; subtitle?: string; downloadHref?: string } | null>(null);

  const sound = useSoundPref();
  const wsRef = useRef<WebSocket | null>(null);
  const toastTimer = useRef<number | undefined>(undefined);

  const notify = useCallback((msg: string) => {
    setToast(msg);
    if (toastTimer.current) window.clearTimeout(toastTimer.current);
    toastTimer.current = window.setTimeout(() => setToast(''), 2600);
  }, []);

  const fetchBootstrap = useCallback(async () => {
    try {
      const res = await apiJSON<RemoteBootstrap>('/api/remote/bootstrap');
      setBoot(res);
    } catch (e) {
      const msg = e instanceof Error ? e.message : '';
      if (msg.includes('令牌') || msg.includes('401')) {
        onTokenInvalid();
      } else {
        notify(msg || '刷新失败');
      }
    }
  }, [notify, onTokenInvalid]);

  useEffect(() => {
    fetchBootstrap();
  }, [fetchBootstrap]);

  useEffect(() => {
    let alive = true;
    let reconnectTimer: number | undefined;
    let hasConnectedOnce = false;

    function connect() {
      if (!alive) return;
      const ws = new WebSocket(wsUrl('remote', deviceName));
      wsRef.current = ws;
      ws.onopen = () => {
        setWsOnline(true);
        if (hasConnectedOnce) fetchBootstrap();
        hasConnectedOnce = true;
      };
      ws.onclose = () => {
        setWsOnline(false);
        if (alive) reconnectTimer = window.setTimeout(connect, 2500);
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          setBoot((prev) => {
            if (!prev) return prev;
            switch (msg.type) {
              case 'transfer_new': {
                const item = msg.data as TransferItem;
                if (item.direction === 'out') {
                  playChime('receive');
                  notify(`电脑发来：${item.name}`);
                }
                return { ...prev, transfers: [item, ...prev.transfers.filter((t) => t.id !== item.id)] };
              }
              case 'transfer_delete':
                return { ...prev, transfers: prev.transfers.filter((t) => t.id !== msg.data.id) };
              case 'transfers_cleared': {
                const dir = msg.data.direction;
                return { ...prev, transfers: dir ? prev.transfers.filter((t) => t.direction !== dir) : [] };
              }
              case 'clipboard_new': {
                const item = msg.data as ClipboardItem;
                if (item.source === 'pc') {
                  playChime('receive');
                  if (item.kind === 'text' && item.text) setLivePCClip(item.text);
                  notify('收到电脑推送的剪贴板');
                }
                return { ...prev, clipboard: [item, ...prev.clipboard.filter((c) => c.id !== item.id)] };
              }
              case 'clipboard_delete':
                return { ...prev, clipboard: prev.clipboard.filter((c) => c.id !== msg.data.id) };
              case 'clipboard_cleared':
                return { ...prev, clipboard: [] };
              case 'config_updated':
                return {
                  ...prev,
                  pcName: msg.data.deviceName || prev.pcName,
                  confirmReceive: typeof msg.data.confirmReceive === 'boolean' ? msg.data.confirmReceive : prev.confirmReceive,
                };
              default:
                return prev;
            }
          });
        } catch {
          // ignore
        }
      };
    }

    connect();
    return () => {
      alive = false;
      if (reconnectTimer) window.clearTimeout(reconnectTimer);
      wsRef.current?.close();
    };
  }, [deviceName, fetchBootstrap, notify]);

  async function sendFiles(files: FileList | File[]) {
    const list = Array.from(files);
    if (list.length === 0) return;

    setTab('files');
    const totalSize = list.reduce((acc, f) => acc + f.size, 0);
    let requestId = '';

    if (boot?.confirmReceive) {
      setUploadState({
        phase: 'waiting',
        percent: 0,
        loaded: 0,
        total: totalSize,
        speedBps: 0,
        fileCount: list.length,
      });
      try {
        const consent = await apiJSON<{ approved: boolean; requestId: string; error?: string }>('/api/remote/request', {
          method: 'POST',
          body: JSON.stringify({
            deviceName,
            totalSize,
            files: list.map((f) => ({ name: f.name, size: f.size, type: f.type })),
          }),
        });
        if (!consent.approved) {
          setUploadState((s) => ({ ...s, phase: 'idle' }));
          notify(consent.error || '电脑端拒绝了此次发送');
          return;
        }
        requestId = consent.requestId;
      } catch (e) {
        setUploadState((s) => ({ ...s, phase: 'idle' }));
        notify(e instanceof Error ? e.message : '等待电脑确认超时');
        return;
      }
    }

    const form = new FormData();
    form.append('deviceName', deviceName);
    for (const f of list) form.append('file', f, f.name);

    setUploadState({
      phase: 'uploading',
      percent: 0,
      loaded: 0,
      total: totalSize,
      speedBps: 0,
      fileCount: list.length,
    });

    const startTime = Date.now();
    const xhr = new XMLHttpRequest();
    xhrRef.current = xhr;

    xhr.upload.onprogress = (ev) => {
      if (!ev.lengthComputable) return;
      const elapsedSec = Math.max(0.15, (Date.now() - startTime) / 1000);
      const pct = Math.min(100, Math.round((ev.loaded / ev.total) * 100));
      setUploadState({
        phase: 'uploading',
        percent: pct,
        loaded: ev.loaded,
        total: ev.total,
        speedBps: ev.loaded / elapsedSec,
        fileCount: list.length,
      });
    };

    xhr.onload = () => {
      xhrRef.current = null;
      setUploadState((s) => ({ ...s, phase: 'idle' }));
      if (xhr.status >= 200 && xhr.status < 300) {
        playChime('send');
        notify(`已发送 ${list.length} 个文件到电脑`);
      } else {
        let msg = `发送失败 (${xhr.status})`;
        try {
          const parsed = JSON.parse(xhr.responseText);
          if (parsed.error) msg = parsed.error;
        } catch {
          // ignore
        }
        notify(msg);
      }
    };

    xhr.onerror = () => {
      xhrRef.current = null;
      setUploadState((s) => ({ ...s, phase: 'idle' }));
      notify('网络中断，发送失败');
    };

    xhr.onabort = () => {
      xhrRef.current = null;
      setUploadState((s) => ({ ...s, phase: 'idle' }));
      notify('已取消发送');
    };

    xhr.open('POST', '/api/remote/upload');
    xhr.withCredentials = true;
    const headers = authHeaders({ 'X-Device': encodeURIComponent(deviceName) });
    if (requestId) headers['X-Request-Id'] = requestId;
    Object.entries(headers).forEach(([k, v]) => xhr.setRequestHeader(k, v));
    xhr.send(form);
  }

  async function sendClipboardText() {
    if (!clipText.trim()) return;
    setSendingClip(true);
    try {
      await apiJSON('/api/clipboard/push', {
        method: 'POST',
        body: JSON.stringify({ text: clipText, deviceName }),
      });
      setClipText('');
      playChime('send');
      notify('已同步到 Windows 剪贴板');
    } catch (e) {
      notify(e instanceof Error ? e.message : '发送失败');
    } finally {
      setSendingClip(false);
    }
  }

  async function sendClipboardImage(file: File) {
    const form = new FormData();
    form.append('file', file, file.name);
    try {
      const res = await fetch('/api/clipboard/push', {
        method: 'POST',
        credentials: 'include',
        headers: authHeaders({ 'X-Device': encodeURIComponent(deviceName) }),
        body: form,
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || '发送失败');
      playChime('send');
      notify('图片已写入 Windows 剪贴板');
    } catch (e) {
      notify(e instanceof Error ? e.message : '发送图片失败');
    }
  }

  async function pullLivePCClipboard() {
    try {
      const res = await apiJSON<{ text?: string }>('/api/clipboard/latest');
      const txt = res.text || '';
      setLivePCClip(txt);
      if (!txt) {
        notify('电脑当前剪贴板暂无文字');
        return;
      }
      const ok = await copyTextSafe(txt);
      playChime('pop');
      notify(ok ? '已复制电脑最新剪贴板' : '已拉取到下方，点复制即可');
    } catch (e) {
      notify(e instanceof Error ? e.message : '拉取失败');
    }
  }

  function saveNickname(next: string) {
    const clean = next.trim() || guessInitialDeviceName();
    setDeviceName(clean);
    setStoredDeviceName(clean);
    setEditingName(false);
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify({ type: 'rename', deviceName: clean }));
    }
  }

  const fromPC = (boot?.transfers || []).filter((t) => t.direction === 'out');
  const sentToPC = (boot?.transfers || []).filter((t) => t.direction === 'in');

  const ringR = 80;
  const ringCirc = 2 * Math.PI * ringR;
  const ringOffset = ringCirc * (1 - uploadState.percent / 100);

  return (
    <div
      className="min-h-screen bg-canvas text-ink-1 max-w-xl mx-auto border-x border-hairline flex flex-col pb-24"
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        if (e.dataTransfer.files?.length) sendFiles(e.dataTransfer.files);
      }}
    >
      <input
        ref={fileInputRef}
        type="file"
        multiple
        className="hidden"
        onChange={(e) => {
          if (e.target.files?.length) {
            sendFiles(e.target.files);
            e.target.value = '';
          }
        }}
      />

      {quickLook && (
        <QuickLookModal
          src={quickLook.src}
          title={quickLook.title}
          subtitle={quickLook.subtitle}
          downloadHref={quickLook.downloadHref}
          onClose={() => setQuickLook(null)}
        />
      )}

      {toast && (
        <div className="fixed top-3 left-1/2 -translate-x-1/2 z-50 px-3.5 py-2 rounded-sm bg-ink-1 text-canvas dark:bg-elevated dark:text-ink-1 border border-hairline-strong text-xs font-medium shadow-xl flex items-center gap-2 max-w-[90vw] animate-sheet">
          <Check className="w-3.5 h-3.5 text-signal shrink-0" />
          <span className="truncate">{toast}</span>
        </div>
      )}

      {/* Top Precision Status Bar */}
      <header className="sticky top-0 z-30 safe-pt bg-surface border-b border-hairline px-4 py-3 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2.5 min-w-0">
          <img src="/icon.svg" alt="AirDropWin" className="w-8 h-8 rounded-sm shrink-0" />
          <div className="min-w-0">
            <div className="flex items-center gap-1.5">
              <span className="text-sm font-semibold text-ink-1 truncate">{boot?.pcName || pcName}</span>
              <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${wsOnline ? 'bg-signal' : 'bg-ink-3'}`} />
            </div>
            {editingName ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  saveNickname(deviceName);
                }}
                className="mt-0.5"
              >
                <input
                  autoFocus
                  value={deviceName}
                  onChange={(e) => setDeviceName(e.target.value)}
                  onBlur={() => saveNickname(deviceName)}
                  className="text-xs px-1.5 py-0.5 rounded-xs bg-canvas border border-signal text-ink-1 w-32 focus:outline-none"
                />
              </form>
            ) : (
              <button
                onClick={() => setEditingName(true)}
                className="inline-flex items-center gap-1 text-[11px] font-mono-tech text-ink-3 hover:text-ink-1 cursor-pointer"
              >
                <span>{deviceName}</span>
                <Pencil className="w-2.5 h-2.5" />
              </button>
            )}
          </div>
        </div>

        <div className="flex items-center gap-1">
          <button
            onClick={sound.toggle}
            className="keycap-btn p-2 rounded-sm border border-hairline bg-elevated text-ink-2 cursor-pointer"
            aria-label="切换音效"
          >
            {sound.enabled ? <Volume2 className="w-3.5 h-3.5" /> : <VolumeX className="w-3.5 h-3.5 opacity-50" />}
          </button>
          <button
            onClick={onToggleTheme}
            className="keycap-btn p-2 rounded-sm border border-hairline bg-elevated text-ink-2 cursor-pointer"
            aria-label="切换主题"
          >
            {isDark ? <Sun className="w-3.5 h-3.5" /> : <Moon className="w-3.5 h-3.5" />}
          </button>
          <button
            onClick={fetchBootstrap}
            className="keycap-btn p-2 rounded-sm border border-hairline bg-elevated text-ink-2 cursor-pointer"
            aria-label="刷新"
          >
            <RefreshCw className="w-3.5 h-3.5" />
          </button>
        </div>
      </header>

      {/* TAB 1: FILES */}
      {tab === 'files' && (
        <div className="flex-1 divide-y divide-hairline">
          {/* Polar Transmit Dial */}
          <section
            onClick={() => uploadState.phase === 'idle' && fileInputRef.current?.click()}
            className={`swiss-dot-grid px-4 py-8 flex flex-col items-center justify-center text-center cursor-pointer transition ${
              dragging ? 'bg-signal-soft' : ''
            }`}
          >
            <div className="relative w-48 h-48 flex items-center justify-center">
              <div className="absolute inset-x-0 top-1/2 h-px bg-[var(--hairline)] pointer-events-none" />
              <div className="absolute inset-y-0 left-1/2 w-px bg-[var(--hairline)] pointer-events-none" />
              <div className="absolute inset-0 rounded-full border border-hairline-strong" />
              <span className="signal-ping" />
              <span className="signal-ping signal-ping-delay" />

              <svg className="absolute inset-0 w-full h-full -rotate-90 pointer-events-none" viewBox="0 0 180 180">
                <circle cx="90" cy="90" r={ringR} fill="none" stroke="var(--hairline)" strokeWidth="3" />
                {uploadState.phase === 'uploading' && (
                  <circle
                    cx="90"
                    cy="90"
                    r={ringR}
                    fill="none"
                    stroke="var(--signal)"
                    strokeWidth="4"
                    strokeLinecap="square"
                    strokeDasharray={ringCirc}
                    strokeDashoffset={ringOffset}
                    className="transition-all duration-150"
                  />
                )}
              </svg>

              {uploadState.phase === 'idle' ? (
                <div className="keycap-btn w-32 h-32 rounded-full bg-signal text-white shadow-xl flex flex-col items-center justify-center gap-1 z-10 ring-4 ring-[var(--canvas)]">
                  <Upload className="w-6 h-6" />
                  <span className="text-xs font-semibold tracking-wide">轻点投送</span>
                  <span className="font-mono-tech text-[9px] opacity-80">PHOTO · FILE</span>
                </div>
              ) : uploadState.phase === 'waiting' ? (
                <div className="w-32 h-32 rounded-full bg-surface border border-hairline-strong flex flex-col items-center justify-center gap-1 z-10 px-2">
                  <Radio className="w-5 h-5 text-signal animate-pulse" />
                  <span className="text-xs font-semibold text-ink-1">等待电脑确认</span>
                  <span className="font-mono-tech text-[10px] text-ink-3">{formatBytes(uploadState.total)}</span>
                </div>
              ) : (
                <div
                  onClick={(e) => e.stopPropagation()}
                  className="w-32 h-32 rounded-full bg-surface border border-hairline-strong flex flex-col items-center justify-center gap-0.5 z-10"
                >
                  <span className="font-mono-tech text-2xl font-bold text-signal leading-none">{uploadState.percent}%</span>
                  <span className="font-mono-tech text-[10px] text-ink-2 mt-0.5">{formatBytes(uploadState.speedBps)}/s</span>
                  <button
                    onClick={() => xhrRef.current?.abort()}
                    className="mt-1 inline-flex items-center gap-0.5 px-2 py-0.5 rounded-xs border border-hairline bg-well text-ink-1 text-[10px] font-medium cursor-pointer"
                  >
                    <X className="w-2.5 h-2.5" /> 取消
                  </button>
                </div>
              )}
            </div>

            <p className="text-xs text-ink-2 mt-4">
              {uploadState.phase === 'idle'
                ? '多选相册原图、4K 视频、Office 文档或 ZIP'
                : `正在发往 ${boot?.pcName || pcName}...`}
            </p>
          </section>

          {/* From PC Ledger */}
          <section className="bg-surface">
            <div className="px-4 py-2.5 border-b border-hairline flex items-center justify-between text-[11px] font-mono-tech text-ink-3 uppercase tracking-wider">
              <span>FROM WINDOWS</span>
              <span>{fromPC.length}</span>
            </div>

            {fromPC.length === 0 ? (
              <div className="p-8 text-center text-xs text-ink-3">
                在电脑拖入文件或右键「发送到 → 隔空传」后，文件会实时推送到这里
              </div>
            ) : (
              <div className="divide-y divide-hairline">
                {fromPC.map((item) => (
                  <div key={item.id} className="px-4 py-3 flex items-center justify-between gap-3">
                    <div className="flex items-center gap-3 min-w-0">
                      {isPreviewableImg(item) ? (
                        <button
                          onClick={() =>
                            setQuickLook({
                              src: fileDownloadUrl(item.id, true),
                              title: item.name,
                              subtitle: `${formatBytes(item.size)} · ${formatTime(item.createdAt)}`,
                              downloadHref: fileDownloadUrl(item.id, false),
                            })
                          }
                          className="w-11 h-11 rounded-xs overflow-hidden border border-hairline bg-canvas shrink-0 cursor-pointer"
                        >
                          <img src={fileDownloadUrl(item.id, true)} alt={item.name} className="w-full h-full object-cover" />
                        </button>
                      ) : (
                        <div className="w-11 h-11 rounded-xs border border-hairline bg-canvas flex items-center justify-center shrink-0">
                          <FileExtStamp name={item.name} />
                        </div>
                      )}
                      <div className="min-w-0">
                        <div className="text-[13px] font-medium text-ink-1 truncate">{item.name}</div>
                        <div className="font-mono-tech text-[11px] text-ink-3 mt-0.5">
                          {formatBytes(item.size)} · {formatTime(item.createdAt)}
                        </div>
                      </div>
                    </div>

                    <a
                      href={fileDownloadUrl(item.id, false)}
                      download={item.name}
                      className="keycap-btn inline-flex items-center gap-1 px-3 py-2 rounded-sm bg-signal text-white text-xs font-semibold shrink-0"
                    >
                      <ArrowDownToLine className="w-3.5 h-3.5" />
                      保存
                    </a>
                  </div>
                ))}
              </div>
            )}
          </section>

          {sentToPC.length > 0 && (
            <section className="bg-surface">
              <div className="px-4 py-2.5 border-b border-hairline flex items-center justify-between text-[11px] font-mono-tech text-ink-3 uppercase tracking-wider">
                <span>RECENTLY SENT</span>
                <span>{sentToPC.length}</span>
              </div>
              <div className="divide-y divide-hairline">
                {sentToPC.slice(0, 10).map((item) => (
                  <div key={item.id} className="px-4 py-2.5 flex items-center justify-between gap-2 text-xs">
                    <span className="truncate text-ink-1">{item.name}</span>
                    <span className="font-mono-tech text-[11px] text-ink-3 shrink-0">
                      {formatBytes(item.size)} · {formatTime(item.createdAt)}
                    </span>
                  </div>
                ))}
              </div>
            </section>
          )}
        </div>
      )}

      {/* TAB 2: CLIPBOARD */}
      {tab === 'clipboard' && (
        <div className="flex-1 divide-y divide-hairline bg-surface">
          <div className="p-4 space-y-3">
            <div className="flex items-center justify-between gap-2">
              <div>
                <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">PULL FROM WINDOWS</div>
                <div className="text-xs font-semibold text-ink-1 mt-0.5">读取电脑当前 Ctrl+C 内容</div>
              </div>
              <button
                onClick={pullLivePCClipboard}
                className="keycap-btn px-3.5 py-2 rounded-sm bg-signal text-white text-xs font-semibold shrink-0 cursor-pointer"
              >
                拉取并复制
              </button>
            </div>
            {livePCClip && (
              <div className="p-3 rounded-sm bg-canvas border border-hairline space-y-2">
                <p className="text-xs text-ink-1 whitespace-pre-wrap break-all">{safePreviewText(livePCClip, 500)}</p>
                <button
                  onClick={async () => {
                    await copyTextSafe(livePCClip);
                    notify('已复制到本机剪贴板');
                  }}
                  className="keycap-btn inline-flex items-center gap-1 px-2.5 py-1 rounded-sm border border-hairline bg-elevated text-[11px] font-medium text-ink-1 cursor-pointer"
                >
                  <Copy className="w-3 h-3" /> 再次复制
                </button>
              </div>
            )}
          </div>

          <div className="p-4 space-y-3">
            <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">PUSH TO WINDOWS</div>
            <textarea
              rows={3}
              value={clipText}
              onChange={(e) => setClipText(e.target.value)}
              placeholder="粘贴或输入文字，发送后电脑直接 Ctrl+V..."
              className="w-full rounded-sm bg-canvas border border-hairline p-3 text-sm text-ink-1 placeholder:text-ink-3 focus:outline-none focus:border-signal resize-none"
            />
            <input
              ref={clipImgInputRef}
              type="file"
              accept="image/*"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) {
                  sendClipboardImage(f);
                  e.target.value = '';
                }
              }}
            />
            <div className="flex gap-2">
              <button
                onClick={sendClipboardText}
                disabled={sendingClip || !clipText.trim()}
                className="keycap-btn flex-1 py-2.5 rounded-sm bg-signal disabled:opacity-35 text-white text-xs font-semibold inline-flex items-center justify-center gap-1.5 cursor-pointer"
              >
                <Send className="w-3.5 h-3.5" />
                {sendingClip ? '发送中...' : '发送文字给电脑'}
              </button>
              <button
                onClick={() => clipImgInputRef.current?.click()}
                className="keycap-btn px-3.5 py-2.5 rounded-sm border border-hairline bg-elevated text-ink-1 text-xs font-medium inline-flex items-center gap-1.5 cursor-pointer"
              >
                <ImageIcon className="w-3.5 h-3.5" />
                发图片
              </button>
            </div>
          </div>

          <div>
            <div className="px-4 py-2.5 border-b border-hairline text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">
              HISTORY · {(boot?.clipboard || []).length}
            </div>
            {(boot?.clipboard || []).length === 0 ? (
              <div className="p-8 text-center text-xs text-ink-3">暂无剪贴板同步记录</div>
            ) : (
              <div className="divide-y divide-hairline">
                {(boot?.clipboard || []).map((c) => (
                  <div key={c.id} className="p-4 space-y-2">
                    <div className="flex items-center justify-between font-mono-tech text-[10px] text-ink-3">
                      <span className="text-ink-2 font-semibold">{c.deviceName}</span>
                      <span>{formatTime(c.createdAt)}</span>
                    </div>
                    {c.kind === 'image' ? (
                      <div className="space-y-2">
                        <button
                          onClick={() =>
                            setQuickLook({
                              src: clipImageUrl(c.id),
                              title: `剪贴板图片 (${c.deviceName})`,
                              subtitle: formatTime(c.createdAt),
                              downloadHref: clipImageUrl(c.id),
                            })
                          }
                          className="w-full rounded-sm overflow-hidden border border-hairline bg-canvas cursor-pointer"
                        >
                          <img src={clipImageUrl(c.id)} alt="剪贴板图片" className="w-full max-h-56 object-contain" />
                        </button>
                        <a
                          href={clipImageUrl(c.id)}
                          download={`clipboard_${c.id}.png`}
                          className="inline-flex items-center gap-1 px-2.5 py-1 rounded-sm border border-hairline bg-elevated text-xs font-medium text-ink-1"
                        >
                          <ArrowDownToLine className="w-3 h-3" /> 保存图片
                        </a>
                      </div>
                    ) : (
                      <>
                        <p className="text-sm text-ink-1 whitespace-pre-wrap break-all">{safePreviewText(c.text, 500)}</p>
                        {(c.text?.length || 0) > 500 && (
                          <p className="font-mono-tech text-[10px] text-ink-3">共 {c.text!.length.toLocaleString()} 字符</p>
                        )}
                        <button
                          onClick={async () => {
                            await copyTextSafe(c.text || '');
                            notify('已复制到本机剪贴板');
                          }}
                          className="keycap-btn inline-flex items-center gap-1 px-2.5 py-1 rounded-sm border border-hairline bg-elevated text-xs font-medium text-ink-1 cursor-pointer"
                        >
                          <Copy className="w-3 h-3" /> 复制全文
                        </button>
                      </>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}

      {/* Bottom Precision Architectural Dock */}
      <div className="fixed bottom-0 inset-x-0 z-40 bg-surface border-t border-hairline safe-pb">
        <nav className="max-w-xl mx-auto grid grid-cols-3 divide-x divide-hairline">
          <button
            onClick={() => setTab('files')}
            className={`py-3 flex items-center justify-center gap-1.5 text-xs font-semibold relative cursor-pointer ${
              tab === 'files' ? 'text-ink-1' : 'text-ink-3'
            }`}
          >
            {tab === 'files' && <span className="absolute top-0 inset-x-6 h-[2px] bg-signal" />}
            <Radio className="w-3.5 h-3.5" />
            <span>隔空传</span>
            {fromPC.length > 0 && (
              <span className="font-mono-tech text-[10px] text-signal">({fromPC.length})</span>
            )}
          </button>

          <button
            onClick={() => uploadState.phase === 'idle' && fileInputRef.current?.click()}
            className="keycap-btn py-3 bg-signal text-white flex items-center justify-center gap-1 text-xs font-semibold cursor-pointer"
          >
            <Plus className="w-4 h-4" />
            <span>发文件</span>
          </button>

          <button
            onClick={() => setTab('clipboard')}
            className={`py-3 flex items-center justify-center gap-1.5 text-xs font-semibold relative cursor-pointer ${
              tab === 'clipboard' ? 'text-ink-1' : 'text-ink-3'
            }`}
          >
            {tab === 'clipboard' && <span className="absolute top-0 inset-x-6 h-[2px] bg-signal" />}
            <Clipboard className="w-3.5 h-3.5" />
            <span>剪贴板</span>
          </button>
        </nav>
      </div>
    </div>
  );
}
