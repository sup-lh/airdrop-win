import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowDownLeft,
  ArrowDownToLine,
  ArrowUpRight,
  Check,
  Clipboard,
  Copy,
  Grid2x2,
  Image as ImageIcon,
  List,
  Moon,
  Pencil,
  Radio,
  RefreshCw,
  Search,
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
  if (/Windows/i.test(ua)) return '另一台 Windows';
  if (/Android/i.test(ua)) return '我的手机';
  return '局域网设备';
}

export default function MobileClient({ pcName, isDark, onToggleTheme, onTokenInvalid }: Props) {
  // Desktop top-level mode ('files' | 'clipboard'), Mobile sub-tab ('fromPC' | 'sent' | 'clipboard')
  const [desktopTab, setDesktopTab] = useState<'files' | 'clipboard'>('files');
  const [mobileTab, setMobileTab] = useState<'fromPC' | 'sent' | 'clipboard'>('fromPC');
  const [desktopFilter, setDesktopFilter] = useState<'all' | 'out' | 'in'>('all');
  const [viewMode, setViewMode] = useState<'list' | 'grid'>('list');
  const [searchQuery, setSearchQuery] = useState('');

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
  const [quickLook, setQuickLook] = useState<{
    src: string;
    title: string;
    subtitle?: string;
    downloadHref?: string;
  } | null>(null);

  const sound = useSoundPref();
  const wsRef = useRef<WebSocket | null>(null);
  const toastTimer = useRef<number | undefined>(undefined);
  const dragDepth = useRef(0);

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
                  notify(`主机发来：${item.name}`);
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
                  notify('收到主机推送的剪贴板');
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
                  confirmReceive:
                    typeof msg.data.confirmReceive === 'boolean' ? msg.data.confirmReceive : prev.confirmReceive,
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
          notify(consent.error || '主机拒绝了此次发送');
          return;
        }
        requestId = consent.requestId;
      } catch (e) {
        setUploadState((s) => ({ ...s, phase: 'idle' }));
        notify(e instanceof Error ? e.message : '等待主机确认超时');
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
        notify(`已发送 ${list.length} 个文件到 ${boot?.pcName || pcName}`);
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
      notify('已写入 Windows 剪贴板，主机可直接 Ctrl+V');
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
      notify('图片已写入 Windows 剪贴板，主机可直接 Ctrl+V');
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
        notify('主机当前剪贴板暂无文字');
        return;
      }
      const ok = await copyTextSafe(txt);
      playChime('pop');
      notify(ok ? '已复制主机最新剪贴板' : '已拉取，点击下方按钮复制');
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

  function handleDragEnter(e: React.DragEvent) {
    e.preventDefault();
    dragDepth.current += 1;
    if (e.dataTransfer.types.includes('Files')) setDragging(true);
  }
  function handleDragLeave(e: React.DragEvent) {
    e.preventDefault();
    dragDepth.current -= 1;
    if (dragDepth.current <= 0) {
      dragDepth.current = 0;
      setDragging(false);
    }
  }
  function handleDrop(e: React.DragEvent) {
    e.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    if (e.dataTransfer.files?.length) sendFiles(e.dataTransfer.files);
  }

  const transfers = boot?.transfers || [];
  const clipboardList = boot?.clipboard || [];
  const fromPC = useMemo(() => transfers.filter((t) => t.direction === 'out'), [transfers]);
  const sentToPC = useMemo(() => transfers.filter((t) => t.direction === 'in'), [transfers]);

  const desktopVisibleTransfers = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return transfers.filter((t) => {
      if (desktopFilter !== 'all' && t.direction !== desktopFilter) return false;
      if (q && !t.name.toLowerCase().includes(q) && !t.deviceName.toLowerCase().includes(q)) return false;
      return true;
    });
  }, [transfers, desktopFilter, searchQuery]);

  const hostTitle = boot?.pcName || pcName;

  // Compact SVG progress ring around 76px core (diameter 148px on mobile, 210px on desktop)
  const renderPolarInstrument = (compactMobile: boolean) => {
    const dialSize = compactMobile ? 'w-[164px] h-[164px]' : 'w-[236px] h-[236px]';
    const coreSize = compactMobile ? 'w-[76px] h-[76px]' : 'w-[84px] h-[84px]';
    const ringR = 82;
    const ringCirc = 2 * Math.PI * ringR;
    const ringOffset = ringCirc * (1 - uploadState.percent / 100);

    return (
      <div
        onClick={() => uploadState.phase === 'idle' && fileInputRef.current?.click()}
        className={`relative swiss-dot-grid flex flex-col items-center justify-center cursor-pointer select-none ${
          compactMobile ? 'px-4 py-6' : 'flex-1 min-h-[320px] p-6'
        }`}
      >
        {!compactMobile && (
          <>
            <div className="absolute top-3 left-4 font-mono-tech text-[10px] text-ink-3 tracking-wider">
              PEER LINK · {hostTitle}
            </div>
            <div className="absolute top-3 right-4 font-mono-tech text-[10px] text-ink-3 tracking-wider">
              {wsOnline ? 'CONNECTED' : 'RECONNECTING'}
            </div>
          </>
        )}

        <div className={`relative ${dialSize} flex items-center justify-center`}>
          {/* Fine Crosshair Axes */}
          <div className="absolute inset-x-0 top-1/2 h-px bg-[var(--hairline)] pointer-events-none" />
          <div className="absolute inset-y-0 left-1/2 w-px bg-[var(--hairline)] pointer-events-none" />

          {/* Calibrated Concentric Rings */}
          <div className="absolute inset-0 rounded-full border border-hairline-strong" />
          <div className="absolute inset-[22%] rounded-full border border-hairline" />

          {!compactMobile && (
            <>
              <span className="absolute -top-4 left-1/2 -translate-x-1/2 font-mono-tech text-[9px] text-ink-3">0°</span>
              <span className="absolute -bottom-4 left-1/2 -translate-x-1/2 font-mono-tech text-[9px] text-ink-3">180°</span>
              <span className="absolute -left-6 top-1/2 -translate-y-1/2 font-mono-tech text-[9px] text-ink-3">270°</span>
              <span className="absolute -right-5 top-1/2 -translate-y-1/2 font-mono-tech text-[9px] text-ink-3">90°</span>
              <div className="vernier-sweep" />
            </>
          )}

          <span className="signal-ping" />
          <span className="signal-ping signal-ping-delay" />

          {/* Precision Circular Progress Ring */}
          <svg className="absolute inset-0 w-full h-full -rotate-90 pointer-events-none" viewBox="0 0 180 180">
            {uploadState.phase === 'uploading' && (
              <circle
                cx="90"
                cy="90"
                r={ringR}
                fill="none"
                stroke="var(--signal)"
                strokeWidth="3.5"
                strokeLinecap="square"
                strokeDasharray={ringCirc}
                strokeDashoffset={ringOffset}
                className="transition-all duration-150"
              />
            )}
          </svg>

          {/* SINGLE 5% SIGNAL VERMILION FOCAL ANCHOR */}
          {uploadState.phase === 'idle' ? (
            <button
              type="button"
              className={`keycap-btn group relative z-20 ${coreSize} rounded-full bg-signal text-white shadow-lg flex flex-col items-center justify-center cursor-pointer ring-4 ring-[var(--canvas)]`}
            >
              <Upload className="w-5 h-5 transition-transform group-hover:-translate-y-0.5" />
              <span className="text-[11px] font-semibold mt-1 tracking-wide">投送文件</span>
            </button>
          ) : uploadState.phase === 'waiting' ? (
            <div className="relative z-20 w-[96px] h-[96px] rounded-full bg-surface border border-hairline-strong flex flex-col items-center justify-center gap-0.5 px-2 text-center shadow-md">
              <Radio className="w-4 h-4 text-signal animate-pulse" />
              <span className="text-[11px] font-semibold text-ink-1">等待确认</span>
              <span className="font-mono-tech text-[9px] text-ink-3">{formatBytes(uploadState.total)}</span>
            </div>
          ) : (
            <div
              onClick={(e) => e.stopPropagation()}
              className="relative z-20 w-[108px] h-[108px] rounded-full bg-surface border border-hairline-strong flex flex-col items-center justify-center gap-0.5 shadow-md"
            >
              <span className="font-mono-tech text-xl font-bold text-signal leading-none">{uploadState.percent}%</span>
              <span className="font-mono-tech text-[10px] text-ink-2">{formatBytes(uploadState.speedBps)}/s</span>
              <button
                onClick={() => xhrRef.current?.abort()}
                className="mt-1 inline-flex items-center gap-0.5 px-2 py-0.5 rounded-xs border border-hairline bg-well text-ink-1 text-[10px] font-medium cursor-pointer"
              >
                <X className="w-2.5 h-2.5" /> 取消
              </button>
            </div>
          )}
        </div>

        <p className={`text-[11px] text-ink-3 text-center ${compactMobile ? 'mt-3.5' : 'mt-6'}`}>
          {uploadState.phase === 'idle'
            ? compactMobile
              ? '轻点选择相册原图、4K 视频、Office 文档或 ZIP'
              : '点击中心按键选择文件，或将文件直接拖入窗口发给主机'
            : `正在传输 ${uploadState.fileCount} 个文件 (${formatBytes(uploadState.loaded)} / ${formatBytes(uploadState.total)})`}
        </p>
      </div>
    );
  };

  const renderTransferRow = (item: TransferItem, showDirectionBadge: boolean) => (
    <div key={item.id} className="px-4 py-3 flex items-center justify-between gap-3 hover:bg-canvas transition">
      <div className="flex items-center gap-3 min-w-0">
        {isPreviewableImg(item) ? (
          <button
            onClick={() =>
              setQuickLook({
                src: fileDownloadUrl(item.id, true),
                title: item.name,
                subtitle: `${formatBytes(item.size)} · ${item.deviceName} · ${formatTime(item.createdAt)}`,
                downloadHref: fileDownloadUrl(item.id, false),
              })
            }
            className="w-10 h-10 rounded-xs overflow-hidden border border-hairline bg-canvas shrink-0 cursor-pointer"
          >
            <img src={fileDownloadUrl(item.id, true)} alt={item.name} className="w-full h-full object-cover" />
          </button>
        ) : (
          <div className="w-10 h-10 rounded-xs border border-hairline bg-canvas flex items-center justify-center shrink-0">
            <FileExtStamp name={item.name} />
          </div>
        )}
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            {showDirectionBadge && (
              <span
                className={`inline-flex items-center gap-0.5 font-mono-tech text-[10px] px-1.5 py-0.2 rounded-xs border shrink-0 ${
                  item.direction === 'out'
                    ? 'border-signal/30 bg-signal-soft text-signal'
                    : 'border-hairline bg-well text-ink-2'
                }`}
              >
                {item.direction === 'out' ? (
                  <>
                    <ArrowDownLeft className="w-2.5 h-2.5" /> 主机发来
                  </>
                ) : (
                  <>
                    <ArrowUpRight className="w-2.5 h-2.5" /> 已发给主机
                  </>
                )}
              </span>
            )}
            <span className="text-[13px] font-medium text-ink-1 truncate">{item.name}</span>
          </div>
          <div className="font-mono-tech text-[11px] text-ink-3 mt-0.5 flex items-center gap-2">
            <span>{formatBytes(item.size)}</span>
            <span>·</span>
            <span>{formatTime(item.createdAt)}</span>
          </div>
        </div>
      </div>

      <a
        href={fileDownloadUrl(item.id, false)}
        download={item.name}
        className="keycap-btn inline-flex items-center gap-1.5 px-3 py-1.5 rounded-sm border border-hairline-strong bg-elevated hover:bg-ink-1 hover:text-canvas hover:border-ink-1 text-ink-1 text-xs font-medium shrink-0"
      >
        <ArrowDownToLine className="w-3.5 h-3.5" />
        <span>保存</span>
      </a>
    </div>
  );

  const renderClipboardComposer = () => (
    <div className="divide-y divide-hairline bg-surface">
      {/* Live Pull Windows Clipboard */}
      <div className="p-4 sm:p-5 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">PULL FROM HOST</div>
            <div className="text-xs font-semibold text-ink-1 mt-0.5">读取 {hostTitle} 当前 Ctrl+C 内容</div>
          </div>
          <button
            onClick={pullLivePCClipboard}
            className="keycap-btn px-3.5 py-2 rounded-sm bg-ink-1 text-canvas dark:bg-elevated dark:text-ink-1 dark:border dark:border-hairline-strong text-xs font-medium shrink-0 cursor-pointer"
          >
            拉取并复制
          </button>
        </div>
        {livePCClip && (
          <div className="p-3 rounded-sm bg-canvas border border-hairline space-y-2">
            <p className="text-xs text-ink-1 whitespace-pre-wrap break-all select-text">{safePreviewText(livePCClip, 500)}</p>
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

      {/* Push Text or Image to Windows */}
      <div className="p-4 sm:p-5 space-y-3">
        <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">PUSH TO HOST CLIPBOARD</div>
        <textarea
          rows={4}
          value={clipText}
          onChange={(e) => setClipText(e.target.value)}
          placeholder={`粘贴或输入文字，发送后 ${hostTitle} 可直接 Ctrl+V 粘贴...`}
          className="w-full rounded-sm bg-canvas border border-hairline p-3 text-xs text-ink-1 placeholder:text-ink-3 leading-relaxed focus:outline-none focus:border-signal resize-none select-text"
        />
        <div className="flex gap-2">
          <button
            onClick={sendClipboardText}
            disabled={sendingClip || !clipText.trim()}
            className="keycap-btn flex-1 py-2.5 rounded-sm bg-signal disabled:opacity-35 text-white text-xs font-semibold inline-flex items-center justify-center gap-1.5 cursor-pointer"
          >
            <Send className="w-3.5 h-3.5" />
            {sendingClip ? '发送中...' : '发送文字给主机'}
          </button>
          <button
            onClick={() => clipImgInputRef.current?.click()}
            className="keycap-btn px-3.5 py-2.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-xs font-medium inline-flex items-center gap-1.5 cursor-pointer"
          >
            <ImageIcon className="w-3.5 h-3.5" />
            发图片
          </button>
        </div>
      </div>
    </div>
  );

  const renderClipboardHistory = () => (
    <div className="divide-y divide-hairline bg-surface">
      {clipboardList.length === 0 ? (
        <div className="p-10 text-center space-y-1">
          <div className="font-mono-tech text-[11px] uppercase tracking-widest text-ink-3">NO CLIPBOARD HISTORY</div>
          <p className="text-xs text-ink-3">在主机按 Ctrl+V 或在左侧发送文本/图片即可实时同步</p>
        </div>
      ) : (
        clipboardList.map((c) => (
          <div key={c.id} className="p-4 space-y-2.5 hover:bg-canvas transition">
            <div className="flex items-center justify-between gap-2 font-mono-tech text-[10px]">
              <div className="flex items-center gap-2">
                <span
                  className={`px-1.5 py-0.2 rounded-xs border ${
                    c.source === 'pc'
                      ? 'border-signal/30 bg-signal-soft text-signal'
                      : 'border-hairline bg-well text-ink-2'
                  }`}
                >
                  {c.deviceName}
                </span>
                <span className="text-ink-3">{formatTime(c.createdAt)}</span>
              </div>
              {c.kind === 'text' ? (
                <button
                  onClick={async () => {
                    await copyTextSafe(c.text || '');
                    notify('已复制到本机剪贴板');
                  }}
                  className="keycap-btn inline-flex items-center gap-1 px-2.5 py-1 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-[11px] font-sans font-medium cursor-pointer"
                >
                  <Copy className="w-3 h-3" /> 复制
                </button>
              ) : (
                <a
                  href={clipImageUrl(c.id)}
                  download={`clipboard_${c.id}.png`}
                  className="keycap-btn inline-flex items-center gap-1 px-2.5 py-1 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-[11px] font-sans font-medium"
                >
                  <ArrowDownToLine className="w-3 h-3" /> 保存
                </a>
              )}
            </div>

            {c.kind === 'image' ? (
              <button
                onClick={() =>
                  setQuickLook({
                    src: clipImageUrl(c.id),
                    title: `剪贴板图片 (${c.deviceName})`,
                    subtitle: formatTime(c.createdAt),
                    downloadHref: clipImageUrl(c.id),
                  })
                }
                className="block rounded-sm overflow-hidden border border-hairline bg-canvas cursor-pointer"
              >
                <img src={clipImageUrl(c.id)} alt="剪贴板图片" className="max-h-52 object-contain" />
              </button>
            ) : (
              <div className="space-y-1 select-text">
                <p className="text-[13px] text-ink-1 whitespace-pre-wrap break-all leading-relaxed">
                  {safePreviewText(c.text, 500)}
                </p>
                {(c.text?.length || 0) > 500 && (
                  <p className="font-mono-tech text-[10px] text-ink-3">共 {c.text!.length.toLocaleString()} 字符</p>
                )}
              </div>
            )}
          </div>
        ))
      )}
    </div>
  );

  return (
    <div
      className="min-h-screen lg:h-screen lg:overflow-hidden bg-canvas text-ink-1 flex flex-col"
      onDragEnter={handleDragEnter}
      onDragOver={(e) => e.preventDefault()}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {/* Hidden File & Clipboard Image Pickers */}
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

      {/* Full-Viewport Drop Sheet (Desktop/Mac Drag & Drop) */}
      {dragging && (
        <div className="fixed inset-0 z-50 bg-canvas/85 backdrop-blur-xs flex items-center justify-center p-6 pointer-events-none">
          <div className="bg-surface border-2 border-dashed border-signal rounded-md p-10 text-center max-w-md w-full space-y-3 animate-sheet shadow-2xl">
            <div className="w-12 h-12 rounded-sm bg-signal text-white flex items-center justify-center mx-auto">
              <Upload className="w-6 h-6" />
            </div>
            <div className="text-[10px] font-mono-tech uppercase tracking-widest text-signal">DROP TO SEND</div>
            <h3 className="text-base font-semibold text-ink-1">松开鼠标，立即发送至 {hostTitle}</h3>
          </div>
        </div>
      )}

      {/* Quick Look Modal */}
      {quickLook && (
        <QuickLookModal
          src={quickLook.src}
          title={quickLook.title}
          subtitle={quickLook.subtitle}
          downloadHref={quickLook.downloadHref}
          onClose={() => setQuickLook(null)}
        />
      )}

      {/* Minimal Status Toast */}
      {toast && (
        <div className="fixed bottom-4 right-4 z-50 px-3.5 py-2 rounded-sm bg-ink-1 text-canvas dark:bg-elevated dark:text-ink-1 border border-hairline-strong text-xs font-medium shadow-xl flex items-center gap-2 max-w-[90vw] animate-sheet">
          <Check className="w-3.5 h-3.5 text-signal shrink-0" />
          <span className="truncate">{toast}</span>
        </div>
      )}

      {/* ====================================================================
          UNIFIED PRECISION TOP HEADER (52px — Desktop & Mobile)
         ==================================================================== */}
      <header className="sticky top-0 z-30 safe-pt h-auto lg:h-[52px] shrink-0 bg-surface border-b border-hairline px-4 lg:px-5 py-2.5 lg:py-0 flex items-center justify-between gap-3">
        <div className="flex items-center gap-2.5 min-w-0">
          <img src="/icon.svg" alt="AirDropWin" className="w-7 h-7 rounded-sm shrink-0" />
          <div className="min-w-0 flex items-center gap-2.5">
            <div className="min-w-0">
              <div className="flex items-center gap-1.5">
                <span className="text-[13px] font-bold tracking-tight text-ink-1 truncate">{hostTitle}</span>
                <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${wsOnline ? 'bg-signal' : 'bg-ink-3'}`} />
              </div>
              {editingName ? (
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    saveNickname(deviceName);
                  }}
                >
                  <input
                    autoFocus
                    value={deviceName}
                    onChange={(e) => setDeviceName(e.target.value)}
                    onBlur={() => saveNickname(deviceName)}
                    className="text-[11px] px-1.5 py-0.5 rounded-xs bg-canvas border border-signal text-ink-1 w-32 focus:outline-none"
                  />
                </form>
              ) : (
                <button
                  onClick={() => setEditingName(true)}
                  className="inline-flex items-center gap-1 text-[10px] font-mono-tech text-ink-3 hover:text-ink-1 cursor-pointer"
                >
                  <span>THIS · {deviceName}</span>
                  <Pencil className="w-2.5 h-2.5" />
                </button>
              )}
            </div>
          </div>
        </div>

        {/* Desktop Top Navigation Tabs (>= 1024px) */}
        <nav className="hidden lg:flex items-center h-full gap-6 text-xs">
          <button
            onClick={() => setDesktopTab('files')}
            className={`h-full inline-flex items-center gap-1.5 border-b-2 transition cursor-pointer ${
              desktopTab === 'files'
                ? 'border-signal text-ink-1 font-semibold'
                : 'border-transparent text-ink-2 hover:text-ink-1'
            }`}
          >
            <span className={`font-mono-tech text-[10px] ${desktopTab === 'files' ? 'text-signal' : 'text-ink-3'}`}>01</span>
            <span>隔空互传</span>
            <span className="font-mono-tech text-[10px] text-ink-3">{transfers.length}</span>
          </button>
          <button
            onClick={() => setDesktopTab('clipboard')}
            className={`h-full inline-flex items-center gap-1.5 border-b-2 transition cursor-pointer ${
              desktopTab === 'clipboard'
                ? 'border-signal text-ink-1 font-semibold'
                : 'border-transparent text-ink-2 hover:text-ink-1'
            }`}
          >
            <span className={`font-mono-tech text-[10px] ${desktopTab === 'clipboard' ? 'text-signal' : 'text-ink-3'}`}>02</span>
            <span>双向剪贴板</span>
            <span className="font-mono-tech text-[10px] text-ink-3">{clipboardList.length}</span>
          </button>
        </nav>

        {/* Right Controls */}
        <div className="flex items-center gap-1.5 shrink-0">
          <button
            onClick={sound.toggle}
            className="keycap-btn p-1.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-2 cursor-pointer"
            title={sound.enabled ? '关闭提示音' : '开启提示音'}
          >
            {sound.enabled ? <Volume2 className="w-3.5 h-3.5" /> : <VolumeX className="w-3.5 h-3.5 opacity-50" />}
          </button>
          <button
            onClick={onToggleTheme}
            className="keycap-btn p-1.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-2 cursor-pointer"
            title={isDark ? '切换到浅色陶瓷' : '切换到深色石墨'}
          >
            {isDark ? <Sun className="w-3.5 h-3.5" /> : <Moon className="w-3.5 h-3.5" />}
          </button>
          <button
            onClick={fetchBootstrap}
            className="keycap-btn p-1.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-2 cursor-pointer"
            title="刷新同步"
          >
            <RefreshCw className="w-3.5 h-3.5" />
          </button>
        </div>
      </header>

      {/* ====================================================================
          DESKTOP / MAC / IPAD LANDSCAPE WORKSPACE (>= 1024px)
          Full-Viewport 1px Swiss Split Shell — ZERO skinny phone strip!
         ==================================================================== */}
      <main className="hidden lg:block flex-1 min-h-0 overflow-hidden">
        {desktopTab === 'files' ? (
          <div className="h-full grid grid-cols-12 divide-x divide-hairline">
            {/* Left 5 Cols: Polar Transmit Instrument + Docked Quick Clipboard Bar */}
            <section className="col-span-5 flex flex-col bg-canvas overflow-hidden">
              {renderPolarInstrument(false)}

              {/* Docked Quick Actions & Live Clipboard Peek */}
              <div className="border-t border-hairline bg-surface p-5 space-y-3 shrink-0">
                <div className="flex items-center justify-between text-[10px] font-mono-tech uppercase tracking-wider text-ink-3">
                  <span>QUICK CLIPBOARD BRIDGE</span>
                  <button
                    onClick={() => setDesktopTab('clipboard')}
                    className="text-ink-2 hover:text-signal cursor-pointer"
                  >
                    打开剪贴板 →
                  </button>
                </div>
                <div className="grid grid-cols-2 gap-2">
                  <button
                    onClick={pullLivePCClipboard}
                    className="keycap-btn py-2 px-3 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-xs font-medium inline-flex items-center justify-center gap-1.5 cursor-pointer"
                  >
                    <Copy className="w-3.5 h-3.5" />
                    拉取主机剪贴板
                  </button>
                  <button
                    onClick={() => clipImgInputRef.current?.click()}
                    className="keycap-btn py-2 px-3 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-xs font-medium inline-flex items-center justify-center gap-1.5 cursor-pointer"
                  >
                    <ImageIcon className="w-3.5 h-3.5" />
                    发图到主机剪贴板
                  </button>
                </div>
              </div>
            </section>

            {/* Right 7 Cols: Full-Height Swiss Transfer Ledger */}
            <section className="col-span-7 flex flex-col bg-surface overflow-hidden">
              <div className="h-[44px] px-4 border-b border-hairline flex items-center justify-between gap-3 shrink-0">
                <div className="flex items-center h-full gap-5 text-xs">
                  <button
                    onClick={() => setDesktopFilter('all')}
                    className={`h-full inline-flex items-center gap-1 border-b-2 cursor-pointer ${
                      desktopFilter === 'all' ? 'border-signal text-ink-1 font-semibold' : 'border-transparent text-ink-2'
                    }`}
                  >
                    全部 <span className="font-mono-tech text-[10px] text-ink-3">{transfers.length}</span>
                  </button>
                  <button
                    onClick={() => setDesktopFilter('out')}
                    className={`h-full inline-flex items-center gap-1 border-b-2 cursor-pointer ${
                      desktopFilter === 'out' ? 'border-signal text-ink-1 font-semibold' : 'border-transparent text-ink-2'
                    }`}
                  >
                    主机发来 <span className="font-mono-tech text-[10px] text-ink-3">{fromPC.length}</span>
                  </button>
                  <button
                    onClick={() => setDesktopFilter('in')}
                    className={`h-full inline-flex items-center gap-1 border-b-2 cursor-pointer ${
                      desktopFilter === 'in' ? 'border-signal text-ink-1 font-semibold' : 'border-transparent text-ink-2'
                    }`}
                  >
                    已发给主机 <span className="font-mono-tech text-[10px] text-ink-3">{sentToPC.length}</span>
                  </button>
                </div>

                <div className="flex items-center gap-2">
                  <div className="relative w-44">
                    <Search className="w-3 h-3 text-ink-3 absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                    <input
                      value={searchQuery}
                      onChange={(e) => setSearchQuery(e.target.value)}
                      placeholder="筛选文件..."
                      className="w-full pl-7 pr-2.5 py-1 rounded-sm bg-canvas border border-hairline text-xs text-ink-1 placeholder:text-ink-3 focus:outline-none focus:border-signal"
                    />
                  </div>
                  <div className="inline-flex border border-hairline rounded-sm overflow-hidden">
                    <button
                      onClick={() => setViewMode('list')}
                      className={`p-1.5 cursor-pointer ${viewMode === 'list' ? 'bg-well text-ink-1' : 'text-ink-3'}`}
                    >
                      <List className="w-3.5 h-3.5" />
                    </button>
                    <button
                      onClick={() => setViewMode('grid')}
                      className={`p-1.5 border-l border-hairline cursor-pointer ${viewMode === 'grid' ? 'bg-well text-ink-1' : 'text-ink-3'}`}
                    >
                      <Grid2x2 className="w-3.5 h-3.5" />
                    </button>
                  </div>
                </div>
              </div>

              <div className="flex-1 overflow-y-auto">
                {desktopVisibleTransfers.length === 0 ? (
                  <div className="h-full flex flex-col items-center justify-center p-8 text-center space-y-1.5">
                    <div className="font-mono-tech text-[11px] uppercase tracking-widest text-ink-3">NO TRANSFERS</div>
                    <p className="text-xs text-ink-2">将任意文件拖入窗口发给 {hostTitle}，或在主机端选择文件推送过来</p>
                  </div>
                ) : viewMode === 'grid' ? (
                  <div className="grid grid-cols-3 gap-px bg-[var(--hairline)] border-b border-hairline">
                    {desktopVisibleTransfers.map((item) => (
                      <div key={item.id} className="bg-surface flex flex-col">
                        <div
                          onClick={() =>
                            isPreviewableImg(item) &&
                            setQuickLook({
                              src: fileDownloadUrl(item.id, true),
                              title: item.name,
                              subtitle: `${formatBytes(item.size)} · ${formatTime(item.createdAt)}`,
                              downloadHref: fileDownloadUrl(item.id, false),
                            })
                          }
                          className="aspect-square bg-canvas flex items-center justify-center overflow-hidden relative cursor-pointer"
                        >
                          {isPreviewableImg(item) ? (
                            <img src={fileDownloadUrl(item.id, true)} alt={item.name} className="w-full h-full object-cover" />
                          ) : (
                            <FileExtStamp name={item.name} large />
                          )}
                          <span className="absolute top-2 left-2 px-1.5 py-0.5 rounded-xs font-mono-tech text-[9px] bg-surface/90 border border-hairline text-ink-1">
                            {item.direction === 'out' ? 'FROM PC' : 'SENT'}
                          </span>
                        </div>
                        <div className="p-2.5 border-t border-hairline flex items-center justify-between gap-2">
                          <div className="min-w-0">
                            <div className="text-xs font-medium text-ink-1 truncate">{item.name}</div>
                            <div className="font-mono-tech text-[10px] text-ink-3">{formatBytes(item.size)}</div>
                          </div>
                          <a
                            href={fileDownloadUrl(item.id, false)}
                            download={item.name}
                            className="p-1.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 shrink-0"
                            title="保存"
                          >
                            <ArrowDownToLine className="w-3.5 h-3.5" />
                          </a>
                        </div>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="divide-y divide-hairline border-b border-hairline">
                    {desktopVisibleTransfers.map((item) => renderTransferRow(item, true))}
                  </div>
                )}
              </div>
            </section>
          </div>
        ) : (
          /* Desktop Clipboard Split (5 : 7) */
          <div className="h-full grid grid-cols-12 divide-x divide-hairline">
            <div className="col-span-5 bg-surface overflow-y-auto">{renderClipboardComposer()}</div>
            <div className="col-span-7 bg-surface flex flex-col overflow-hidden">
              <div className="h-[44px] px-4 border-b border-hairline flex items-center justify-between shrink-0">
                <span className="text-xs font-semibold text-ink-1">
                  剪贴板同步历史 <span className="font-mono-tech text-ink-3 ml-1">{clipboardList.length}</span>
                </span>
              </div>
              <div className="flex-1 overflow-y-auto">{renderClipboardHistory()}</div>
            </div>
          </div>
        )}
      </main>

      {/* ====================================================================
          MOBILE / PORTRAIT POCKET INSTRUMENT (< 1024px)
          Zero bulky bottom orange bricks — clean top polar dial + sticky 1px tabs!
         ==================================================================== */}
      <main className="lg:hidden flex-1 flex flex-col safe-pb">
        {/* Compact Calibrated Polar Dial */}
        <section className="border-b border-hairline bg-canvas">{renderPolarInstrument(true)}</section>

        {/* Clean 1px Architectural Segmented Underline Tabs */}
        <div className="sticky top-[53px] z-20 bg-surface border-b border-hairline grid grid-cols-3 text-xs">
          <button
            onClick={() => setMobileTab('fromPC')}
            className={`py-3 flex items-center justify-center gap-1 border-b-2 transition cursor-pointer ${
              mobileTab === 'fromPC' ? 'border-signal text-ink-1 font-semibold' : 'border-transparent text-ink-2'
            }`}
          >
            <span>电脑发来</span>
            <span className="font-mono-tech text-[10px] text-ink-3">{fromPC.length}</span>
          </button>
          <button
            onClick={() => setMobileTab('sent')}
            className={`py-3 flex items-center justify-center gap-1 border-b-2 transition cursor-pointer ${
              mobileTab === 'sent' ? 'border-signal text-ink-1 font-semibold' : 'border-transparent text-ink-2'
            }`}
          >
            <span>已发出</span>
            <span className="font-mono-tech text-[10px] text-ink-3">{sentToPC.length}</span>
          </button>
          <button
            onClick={() => setMobileTab('clipboard')}
            className={`py-3 flex items-center justify-center gap-1 border-b-2 transition cursor-pointer ${
              mobileTab === 'clipboard' ? 'border-signal text-ink-1 font-semibold' : 'border-transparent text-ink-2'
            }`}
          >
            <Clipboard className="w-3 h-3" />
            <span>剪贴板</span>
          </button>
        </div>

        {/* Mobile Tab Content */}
        <div className="flex-1 bg-surface">
          {mobileTab === 'fromPC' && (
            fromPC.length === 0 ? (
              <div className="p-10 text-center space-y-1.5">
                <div className="font-mono-tech text-[11px] uppercase tracking-widest text-ink-3">WAITING FOR HOST</div>
                <p className="text-xs text-ink-3 leading-relaxed">
                  在 {hostTitle} 拖入文件或右键「发送到 → 隔空传」后，文件会瞬间出现在这里
                </p>
              </div>
            ) : (
              <div className="divide-y divide-hairline border-b border-hairline">
                {fromPC.map((item) => renderTransferRow(item, false))}
              </div>
            )
          )}

          {mobileTab === 'sent' && (
            sentToPC.length === 0 ? (
              <div className="p-10 text-center space-y-1.5">
                <div className="font-mono-tech text-[11px] uppercase tracking-widest text-ink-3">NO SENT FILES</div>
                <p className="text-xs text-ink-3">轻点上方信号按钮，即可选择相册照片、视频或文件发往电脑</p>
              </div>
            ) : (
              <div className="divide-y divide-hairline border-b border-hairline">
                {sentToPC.map((item) => renderTransferRow(item, false))}
              </div>
            )
          )}

          {mobileTab === 'clipboard' && (
            <div className="divide-y divide-hairline">
              {renderClipboardComposer()}
              <div>
                <div className="px-4 py-2.5 border-b border-hairline bg-canvas text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">
                  HISTORY · {clipboardList.length}
                </div>
                {renderClipboardHistory()}
              </div>
            </div>
          )}
        </div>
      </main>
    </div>
  );
}
