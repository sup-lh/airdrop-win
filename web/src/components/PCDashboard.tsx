import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowDownLeft,
  ArrowUpRight,
  Check,
  Clipboard,
  ClipboardCopy,
  Copy,
  ExternalLink,
  FolderOpen,
  Grid2x2,
  Laptop,
  List,
  Monitor,
  Moon,
  QrCode,
  Radio,
  RefreshCw,
  Search,
  Send,
  Settings,
  ShieldAlert,
  ShieldCheck,
  Smartphone,
  Sun,
  Tablet,
  Terminal,
  Trash2,
  Upload,
  Volume2,
  VolumeX,
  X,
} from 'lucide-react';
import {
  apiJSON,
  clipImageUrl,
  ClipboardItem,
  ClientInfo,
  copyTextSafe,
  fileDownloadUrl,
  formatBytes,
  formatTime,
  LocalBootstrap,
  PendingRequest,
  ShortcutLog,
  TransferItem,
  wsUrl,
} from '../api';
import { playChime, useSoundPref } from '../utils/sound';
import QuickLookModal from './QuickLookModal';
import ShortcutGuide from './ShortcutGuide';

type TabKey = 'transfer' | 'clipboard' | 'shortcuts' | 'settings';

interface Props {
  isDark: boolean;
  onToggleTheme: () => void;
}

const TAB_META: Record<TabKey, { idx: string; label: string; en: string }> = {
  transfer: { idx: '01', label: '隔空传输台', en: 'TRANSFER RADAR' },
  clipboard: { idx: '02', label: '双向剪贴板', en: 'CLIPBOARD SYNC' },
  shortcuts: { idx: '03', label: '苹果快捷指令', en: 'APPLE SHORTCUTS' },
  settings: { idx: '04', label: '系统与网络', en: 'SYSTEM SPEC' },
};

export default function PCDashboard({ isDark, onToggleTheme }: Props) {
  const [data, setData] = useState<LocalBootstrap | null>(null);
  const [tab, setTab] = useState<TabKey>('transfer');
  const [filterDir, setFilterDir] = useState<'all' | 'in' | 'out'>('all');
  const [viewMode, setViewMode] = useState<'list' | 'grid'>('list');
  const [searchQuery, setSearchQuery] = useState('');
  const [qrMode, setQrMode] = useState<'ip' | 'mdns'>('ip');
  const [qrBust, setQrBust] = useState(0);
  const [showQRModal, setShowQRModal] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [uploadingOutbox, setUploadingOutbox] = useState(false);
  const [clipInput, setClipInput] = useState('');
  const [toast, setToast] = useState<string>('');
  const [wsOnline, setWsOnline] = useState(false);
  const [deviceNameDraft, setDeviceNameDraft] = useState('');
  const [savingName, setSavingName] = useState(false);
  const [quickLook, setQuickLook] = useState<{
    src: string;
    title: string;
    subtitle?: string;
    transferId?: string;
    downloadHref?: string;
  } | null>(null);

  const sound = useSoundPref();
  const toastTimer = useRef<number | undefined>(undefined);
  const dragDepth = useRef(0);

  const notify = useCallback((msg: string) => {
    setToast(msg);
    if (toastTimer.current) window.clearTimeout(toastTimer.current);
    toastTimer.current = window.setTimeout(() => setToast(''), 2800);
  }, []);

  const loadBootstrap = useCallback(async () => {
    try {
      const boot = await apiJSON<LocalBootstrap>('/api/local/bootstrap');
      setData(boot);
      setDeviceNameDraft(boot.config.deviceName);
      setQrBust((v) => v + 1);
    } catch (e) {
      notify(e instanceof Error ? e.message : '加载状态失败');
    }
  }, [notify]);

  useEffect(() => {
    loadBootstrap();
  }, [loadBootstrap]);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let reconnectTimer: number | undefined;
    let alive = true;
    let hasConnectedOnce = false;

    function connect() {
      if (!alive) return;
      ws = new WebSocket(wsUrl('pc', 'Windows-PC'));
      ws.onopen = () => {
        setWsOnline(true);
        if (hasConnectedOnce) loadBootstrap();
        hasConnectedOnce = true;
      };
      ws.onclose = () => {
        setWsOnline(false);
        if (alive) reconnectTimer = window.setTimeout(connect, 2000);
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.type === 'transfer_new' && msg.data?.direction === 'in') {
            playChime('receive');
          } else if (msg.type === 'clipboard_new' && msg.data?.source === 'remote') {
            playChime('receive');
          } else if (msg.type === 'pending_request') {
            playChime('pop');
          }

          setData((prev) => {
            if (!prev) return prev;
            switch (msg.type) {
              case 'devices':
                return { ...prev, devices: msg.data as ClientInfo[] };
              case 'transfer_new': {
                const item = msg.data as TransferItem;
                return { ...prev, transfers: [item, ...prev.transfers.filter((t) => t.id !== item.id)] };
              }
              case 'transfer_delete':
                return { ...prev, transfers: prev.transfers.filter((t) => t.id !== msg.data.id) };
              case 'transfers_cleared': {
                const dir = msg.data.direction;
                return {
                  ...prev,
                  transfers: dir ? prev.transfers.filter((t) => t.direction !== dir) : [],
                };
              }
              case 'clipboard_new': {
                const item = msg.data as ClipboardItem;
                return { ...prev, clipboard: [item, ...prev.clipboard.filter((c) => c.id !== item.id)] };
              }
              case 'clipboard_delete':
                return { ...prev, clipboard: prev.clipboard.filter((c) => c.id !== msg.data.id) };
              case 'clipboard_cleared':
                return { ...prev, clipboard: [] };
              case 'pending_request': {
                const req = msg.data as PendingRequest;
                return { ...prev, pending: [req, ...prev.pending.filter((p) => p.id !== req.id)] };
              }
              case 'pending_resolved':
                return { ...prev, pending: prev.pending.filter((p) => p.id !== msg.data.id) };
              case 'shortcut_log': {
                const log = msg.data as ShortcutLog;
                return { ...prev, shortcutLogs: [log, ...prev.shortcutLogs].slice(0, 30) };
              }
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
      ws?.close();
    };
  }, [loadBootstrap]);

  const pickNativeFiles = useCallback(async () => {
    try {
      const res = await apiJSON<{ items: TransferItem[] }>('/api/local/pick-files', { method: 'POST' });
      if (res.items?.length) {
        playChime('send');
        notify(`已零拷贝加入 ${res.items.length} 个文件，手机/Mac 立即可下载`);
      }
    } catch (e) {
      notify(e instanceof Error ? e.message : '选择文件失败');
    }
  }, [notify]);

  const pushPCClipboard = useCallback(
    async (explicitText?: string) => {
      try {
        const res = await apiJSON<{ item: ClipboardItem }>('/api/local/clipboard/push', {
          method: 'POST',
          body: JSON.stringify({ text: explicitText || '' }),
        });
        if (explicitText) setClipInput('');
        playChime('send');
        notify(res.item.kind === 'image' ? '已推送 Windows 截图到手机/Mac' : '已推送剪贴板文本到手机/Mac');
      } catch (e) {
        notify(e instanceof Error ? e.message : '推送剪贴板失败');
      }
    },
    [notify]
  );

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setShowQRModal(false);
        setQuickLook(null);
        return;
      }
      const tag = (e.target as HTMLElement | null)?.tagName;
      const isEditing = tag === 'INPUT' || tag === 'TEXTAREA' || (e.target as HTMLElement | null)?.isContentEditable;
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'o') {
        e.preventDefault();
        pickNativeFiles();
        return;
      }
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'v' && !isEditing) {
        e.preventDefault();
        pushPCClipboard();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [pickNativeFiles, pushPCClipboard]);

  async function uploadOutboxFiles(files: FileList | File[]) {
    const arr = Array.from(files);
    if (arr.length === 0) return;
    setUploadingOutbox(true);
    try {
      const form = new FormData();
      for (const f of arr) {
        form.append('file', f, f.name);
      }
      const res = await fetch('/api/local/upload-outbox', { method: 'POST', body: form });
      const json = await res.json();
      if (!res.ok) throw new Error(json.error || '添加失败');
      playChime('send');
      notify(`已将 ${arr.length} 个文件推送到发送队列`);
    } catch (e) {
      notify(e instanceof Error ? e.message : '发送失败');
    } finally {
      setUploadingOutbox(false);
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
    if (e.dataTransfer.files?.length) uploadOutboxFiles(e.dataTransfer.files);
  }

  async function copyClipboardToPC(id: string) {
    try {
      await apiJSON(`/api/local/clipboard/copy/${encodeURIComponent(id)}`, { method: 'POST' });
      playChime('pop');
      notify('已写入 Windows 剪贴板，可直接 Ctrl+V');
    } catch (e) {
      notify(e instanceof Error ? e.message : '复制失败');
    }
  }

  async function updateConfig(patch: Partial<LocalBootstrap['config']>) {
    try {
      await apiJSON('/api/local/config', { method: 'POST', body: JSON.stringify(patch) });
      await loadBootstrap();
      notify('配置已保存');
    } catch (e) {
      notify(e instanceof Error ? e.message : '保存失败');
    }
  }

  async function decidePending(id: string, accept: boolean) {
    try {
      await apiJSON(`/api/local/pending/${encodeURIComponent(id)}/decision`, {
        method: 'POST',
        body: JSON.stringify({ accept }),
      });
      notify(accept ? '已确认接收' : '已拒绝请求');
    } catch (e) {
      notify(e instanceof Error ? e.message : '操作失败');
    }
  }

  async function allowFirewall() {
    try {
      await apiJSON('/api/local/firewall/allow', { method: 'POST' });
      notify('已发起防火墙放行（请在 UAC 弹窗点击确认）');
      setTimeout(loadBootstrap, 2500);
    } catch (e) {
      notify(e instanceof Error ? e.message : '防火墙放行失败');
    }
  }

  async function toggleAutostart() {
    try {
      const res = await apiJSON<{ autostart: boolean }>('/api/local/toggle-autostart', { method: 'POST' });
      setData((p) => (p ? { ...p, autostart: res.autostart } : p));
      notify(res.autostart ? '已开启开机静默自启' : '已关闭开机自启');
    } catch (e) {
      notify(e instanceof Error ? e.message : '设置失败');
    }
  }

  async function toggleSendTo() {
    try {
      const res = await apiJSON<{ sendTo: boolean }>('/api/local/toggle-sendto', { method: 'POST' });
      setData((p) => (p ? { ...p, sendTo: res.sendTo } : p));
      notify(res.sendTo ? '已添加到右键「发送到 → 隔空传」' : '已移除右键「发送到」菜单');
    } catch (e) {
      notify(e instanceof Error ? e.message : '设置失败');
    }
  }

  async function pickFolder() {
    try {
      const res = await apiJSON<{ saveDir: string }>('/api/local/pick-folder', { method: 'POST' });
      if (res.saveDir) {
        await loadBootstrap();
        notify('保存目录已更新');
      }
    } catch (e) {
      notify(e instanceof Error ? e.message : '切换目录失败');
    }
  }

  async function resetToken() {
    if (!window.confirm('重置配对令牌后，所有已配对设备与快捷指令需重新配对，确定继续？')) return;
    try {
      await apiJSON('/api/local/token/reset', { method: 'POST' });
      await loadBootstrap();
      notify('配对令牌已重置并断开旧设备');
    } catch (e) {
      notify(e instanceof Error ? e.message : '重置失败');
    }
  }

  const visibleTransfers = useMemo(() => {
    if (!data) return [];
    const q = searchQuery.trim().toLowerCase();
    return data.transfers.filter((t) => {
      if (filterDir !== 'all' && t.direction !== filterDir) return false;
      if (q && !t.name.toLowerCase().includes(q) && !t.deviceName.toLowerCase().includes(q)) return false;
      return true;
    });
  }, [data, filterDir, searchQuery]);

  if (!data) {
    return (
      <div className="h-screen bg-canvas flex items-center justify-center text-xs font-mono-tech text-ink-3 uppercase tracking-widest">
        LOADING WORKSPACE...
      </div>
    );
  }

  const activeUrl = qrMode === 'mdns' && data.mdnsPairUrl ? data.mdnsPairUrl : data.pairUrl;
  const activePending = data.pending[0] || null;
  const hasDevices = data.devices.length > 0;
  const currentMeta = TAB_META[tab];

  return (
    <div
      className="h-screen w-screen overflow-hidden flex bg-canvas text-ink-1 select-none"
      onDragEnter={handleDragEnter}
      onDragOver={(e) => e.preventDefault()}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {/* Full-Viewport Precision Drop Overlay */}
      {dragging && (
        <div className="fixed inset-0 z-50 bg-canvas/85 backdrop-blur-xs flex items-center justify-center p-6 pointer-events-none">
          <div className="bg-surface border-2 border-dashed border-signal rounded-md p-10 text-center max-w-md w-full space-y-3 animate-sheet shadow-2xl">
            <div className="w-12 h-12 rounded-sm bg-signal text-white flex items-center justify-center mx-auto">
              <Upload className="w-6 h-6" />
            </div>
            <div className="text-[10px] font-mono-tech uppercase tracking-widest text-signal">DROP TO TRANSMIT</div>
            <h3 className="text-lg font-semibold text-ink-1">松开鼠标，立即推送至局域网队列</h3>
            <p className="text-xs text-ink-2">
              {hasDevices
                ? `实时通知 ${data.devices.length} 台在线设备（${data.devices.map((d) => d.deviceName).join(' · ')}）`
                : '文件将立即进入队列，手机/Mac 扫码即可高速下载'}
            </p>
          </div>
        </div>
      )}

      {/* Pending Receive Consent Sheet */}
      {activePending && (
        <div className="fixed inset-0 z-50 bg-black/55 backdrop-blur-xs flex items-center justify-center p-4">
          <div className="bg-surface border border-hairline-strong rounded-md max-w-md w-full overflow-hidden shadow-2xl animate-sheet">
            <div className="px-5 py-4 border-b border-hairline bg-elevated flex items-center justify-between">
              <div>
                <div className="text-[10px] font-mono-tech uppercase tracking-widest text-signal">INCOMING REQUEST</div>
                <h3 className="text-sm font-semibold text-ink-1 mt-0.5">
                  「{activePending.deviceName}」请求发送 {activePending.files.length} 个文件
                </h3>
              </div>
              <span className="font-mono-tech text-xs text-ink-2">{formatBytes(activePending.totalSize)}</span>
            </div>

            <div className="max-h-48 overflow-y-auto divide-y divide-hairline bg-canvas">
              {activePending.files.map((f, idx) => (
                <div key={idx} className="px-5 py-2.5 flex items-center justify-between gap-3 text-xs">
                  <span className="truncate text-ink-1 font-medium">{f.name}</span>
                  <span className="font-mono-tech text-[11px] text-ink-3 shrink-0">{formatBytes(f.size)}</span>
                </div>
              ))}
            </div>

            <div className="px-5 py-3.5 border-t border-hairline bg-surface flex items-center justify-between gap-3">
              <span className="text-[11px] font-mono-tech text-ink-3">SRC {activePending.remoteIp}</span>
              <div className="flex items-center gap-2">
                <button
                  onClick={() => decidePending(activePending.id, false)}
                  className="keycap-btn px-4 py-2 rounded-sm border border-hairline bg-elevated hover:bg-well text-xs font-medium text-ink-1 cursor-pointer"
                >
                  拒绝
                </button>
                <button
                  onClick={() => decidePending(activePending.id, true)}
                  className="keycap-btn px-4 py-2 rounded-sm bg-signal text-white text-xs font-semibold cursor-pointer"
                >
                  接收并保存
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* On-Demand QR Pairing Modal */}
      {showQRModal && (
        <div
          onClick={() => setShowQRModal(false)}
          className="fixed inset-0 z-50 bg-black/55 backdrop-blur-xs flex items-center justify-center p-4"
        >
          <div
            onClick={(e) => e.stopPropagation()}
            className="bg-surface border border-hairline-strong rounded-md max-w-sm w-full overflow-hidden shadow-2xl animate-sheet"
          >
            <div className="px-4 py-3 border-b border-hairline bg-elevated flex items-center justify-between">
              <div className="flex items-center gap-2">
                <QrCode className="w-3.5 h-3.5 text-signal" />
                <span className="text-xs font-semibold text-ink-1">扫码配对新设备</span>
              </div>
              <button
                onClick={() => setShowQRModal(false)}
                className="keycap-btn p-1 rounded-sm hover:bg-well text-ink-2 cursor-pointer"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="p-5">
              <QRMatrixBlock
                data={data}
                qrMode={qrMode}
                setQrMode={setQrMode}
                qrBust={qrBust}
                activeUrl={activeUrl}
                onRefresh={loadBootstrap}
                onCopyLink={async () => {
                  await copyTextSafe(activeUrl);
                  notify('已复制配对链接，可直接发给 Mac 浏览器打开');
                }}
              />
            </div>
          </div>
        </div>
      )}

      {/* Quick Look Image Lightbox */}
      {quickLook && (
        <QuickLookModal
          src={quickLook.src}
          title={quickLook.title}
          subtitle={quickLook.subtitle}
          downloadHref={quickLook.downloadHref}
          onOpenNative={
            quickLook.transferId
              ? () => apiJSON(`/api/local/open/${encodeURIComponent(quickLook.transferId!)}`, { method: 'POST' }).catch((e) => notify(e.message))
              : undefined
          }
          onRevealNative={
            quickLook.transferId
              ? () => apiJSON(`/api/local/reveal/${encodeURIComponent(quickLook.transferId!)}`, { method: 'POST' }).catch((e) => notify(e.message))
              : undefined
          }
          onClose={() => setQuickLook(null)}
        />
      )}

      {/* Minimal Status Toast */}
      {toast && (
        <div className="fixed bottom-4 right-4 z-50 px-3.5 py-2 rounded-sm bg-ink-1 text-canvas dark:bg-elevated dark:text-ink-1 border border-hairline-strong text-xs font-medium shadow-xl flex items-center gap-2 animate-sheet">
          <Check className="w-3.5 h-3.5 text-signal shrink-0" />
          <span>{toast}</span>
        </div>
      )}

      {/* ====================================================================
          LEFT FIXED NAVIGATION & TELEMETRY RAIL (232px)
         ==================================================================== */}
      <aside className="w-[232px] shrink-0 border-r border-hairline bg-surface flex flex-col justify-between">
        <div className="flex flex-col min-h-0 flex-1">
          {/* Brand Header (Exact 52px height aligned with Command Bar) */}
          <div className="h-[52px] px-4 border-b border-hairline flex items-center justify-between shrink-0">
            <div className="flex items-center gap-2.5 min-w-0">
              <img src="/icon.svg" alt="AirDropWin" className="w-7 h-7 rounded-sm shrink-0" />
              <div className="min-w-0">
                <div className="text-[13px] font-bold tracking-tight text-ink-1 leading-none truncate">
                  AirDropWin
                </div>
                <div className="text-[10px] font-mono-tech text-ink-3 mt-1 truncate">
                  {data.config.deviceName}
                </div>
              </div>
            </div>
            <span
              className={`w-2 h-2 rounded-full shrink-0 ${
                hasDevices ? 'bg-signal' : wsOnline ? 'bg-[var(--ok)]' : 'bg-ink-3'
              }`}
              title={hasDevices ? `${data.devices.length} 台设备已连接` : '局域网监听中'}
            />
          </div>

          {/* Primary Navigation List */}
          <nav className="py-2 border-b border-hairline">
            <RailNavItem
              idx="01"
              label="隔空传输台"
              count={data.transfers.length}
              active={tab === 'transfer'}
              icon={<Radio className="w-3.5 h-3.5" />}
              onClick={() => setTab('transfer')}
            />
            <RailNavItem
              idx="02"
              label="双向剪贴板"
              count={data.clipboard.length}
              active={tab === 'clipboard'}
              icon={<Clipboard className="w-3.5 h-3.5" />}
              onClick={() => setTab('clipboard')}
            />
            <RailNavItem
              idx="03"
              label="苹果快捷指令"
              count={data.shortcutLogs.length || undefined}
              active={tab === 'shortcuts'}
              icon={<Terminal className="w-3.5 h-3.5" />}
              onClick={() => setTab('shortcuts')}
            />
            <RailNavItem
              idx="04"
              label="系统与网络"
              active={tab === 'settings'}
              icon={<Settings className="w-3.5 h-3.5" />}
              onClick={() => setTab('settings')}
            />
          </nav>

          {/* Online Peer Presence Section */}
          <div className="flex-1 overflow-y-auto px-4 py-3.5 space-y-2.5">
            <div className="flex items-center justify-between text-[10px] font-mono-tech uppercase tracking-wider text-ink-3">
              <span>ONLINE PEERS</span>
              <span>{data.devices.length}</span>
            </div>

            {data.devices.length === 0 ? (
              <div className="border border-dashed border-hairline rounded-sm p-3 text-[11px] text-ink-3 leading-relaxed">
                暂无在线设备。使用 iPhone 相机扫描右侧二维码即可实时接入。
              </div>
            ) : (
              <div className="space-y-1.5">
                {data.devices.map((dev) => (
                  <button
                    key={dev.id}
                    onClick={pickNativeFiles}
                    title="点击选择文件发给此设备"
                    className="keycap-btn w-full flex items-center justify-between gap-2 px-2.5 py-2 rounded-sm border border-hairline bg-elevated hover:border-hairline-strong text-left cursor-pointer"
                  >
                    <div className="flex items-center gap-2 min-w-0">
                      <span className="text-ink-2 shrink-0">
                        <PlatformIcon platform={dev.platform} small />
                      </span>
                      <div className="min-w-0">
                        <div className="text-xs font-medium text-ink-1 truncate">{dev.deviceName}</div>
                        <div className="text-[10px] font-mono-tech text-ink-3">{dev.ip}</div>
                      </div>
                    </div>
                    <span className="w-1.5 h-1.5 rounded-full bg-signal shrink-0" />
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>

        {/* Bottom Hardware Telemetry & Utilities */}
        <div className="border-t border-hairline bg-elevated">
          {/* Live Network Telemetry */}
          <div className="px-4 py-3 space-y-1.5 border-b border-hairline">
            <div className="flex items-center justify-between text-[10px] font-mono-tech uppercase tracking-wider text-ink-3">
              <span>{data.activeAdapter.name}</span>
              <button
                onClick={loadBootstrap}
                title="刷新网卡"
                className="hover:text-ink-1 cursor-pointer"
              >
                <RefreshCw className="w-3 h-3" />
              </button>
            </div>
            <button
              onClick={async () => {
                await copyTextSafe(data.pairUrl);
                notify('已复制局域网配对地址');
              }}
              title="点击复制局域网配对链接"
              className="block w-full text-left font-mono-tech text-xs font-medium text-ink-1 hover:text-signal transition truncate cursor-pointer"
            >
              {data.activeAdapter.ip}:{data.port}
            </button>
            {data.mdnsHost && (
              <div className="font-mono-tech text-[10px] text-ink-3 truncate">{data.mdnsHost}</div>
            )}
          </div>

          {/* Firewall Status Indicator */}
          {!data.firewallOk ? (
            <button
              onClick={allowFirewall}
              className="w-full px-4 py-2.5 border-b border-hairline bg-signal-soft text-signal text-[11px] font-semibold flex items-center justify-between hover:opacity-90 cursor-pointer"
            >
              <span className="inline-flex items-center gap-1.5">
                <ShieldAlert className="w-3.5 h-3.5" />
                防火墙未放行
              </span>
              <span className="underline">一键放行</span>
            </button>
          ) : (
            <div className="px-4 py-2 border-b border-hairline flex items-center justify-between text-[11px] text-ink-3">
              <span className="inline-flex items-center gap-1.5">
                <ShieldCheck className="w-3.5 h-3.5 text-[var(--ok)]" />
                防火墙已放行
              </span>
              <span className="font-mono-tech text-[10px]">TCP {data.port}</span>
            </div>
          )}

          {/* Theme & Sound Footer Switches */}
          <div className="grid grid-cols-2 divide-x divide-hairline text-[11px] text-ink-2">
            <button
              onClick={onToggleTheme}
              className="py-2.5 flex items-center justify-center gap-1.5 hover:bg-well hover:text-ink-1 transition cursor-pointer"
            >
              {isDark ? <Sun className="w-3.5 h-3.5" /> : <Moon className="w-3.5 h-3.5" />}
              <span>{isDark ? '浅色陶瓷' : '深色石墨'}</span>
            </button>
            <button
              onClick={sound.toggle}
              className="py-2.5 flex items-center justify-center gap-1.5 hover:bg-well hover:text-ink-1 transition cursor-pointer"
            >
              {sound.enabled ? <Volume2 className="w-3.5 h-3.5" /> : <VolumeX className="w-3.5 h-3.5 opacity-50" />}
              <span>{sound.enabled ? '提示音开' : '已静音'}</span>
            </button>
          </div>
        </div>
      </aside>

      {/* ====================================================================
          MAIN RIGHT WORKSPACE SHELL
         ==================================================================== */}
      <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
        {/* Top Precision Command Bar (52px) */}
        <header className="h-[52px] shrink-0 border-b border-hairline bg-surface px-5 flex items-center justify-between gap-4">
          <div className="flex items-center gap-3 min-w-0">
            <span className="font-mono-tech text-[11px] text-ink-3">{currentMeta.idx}</span>
            <h1 className="text-sm font-semibold text-ink-1 tracking-tight">{currentMeta.label}</h1>
            <span className="hidden md:inline text-[10px] font-mono-tech tracking-widest text-ink-3 border-l border-hairline pl-3">
              {currentMeta.en}
            </span>
          </div>

          <div className="flex items-center gap-2 shrink-0">
            <div className="hidden xl:flex items-center gap-2 text-[11px] text-ink-3 mr-2 font-mono-tech">
              <kbd className="px-1.5 py-0.5 rounded-xs border border-hairline bg-canvas text-ink-2">Ctrl+O</kbd>
              <span>选文件</span>
              <kbd className="px-1.5 py-0.5 rounded-xs border border-hairline bg-canvas text-ink-2 ml-1">Ctrl+V</kbd>
              <span>推剪贴板</span>
            </div>

            <button
              onClick={() => setShowQRModal(true)}
              className="keycap-btn inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-xs font-medium text-ink-1 cursor-pointer"
            >
              <QrCode className="w-3.5 h-3.5" />
              <span>扫码配对</span>
            </button>

            <button
              onClick={() => apiJSON('/api/local/open-savedir', { method: 'POST' })}
              className="keycap-btn inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-xs font-medium text-ink-1 cursor-pointer"
            >
              <FolderOpen className="w-3.5 h-3.5" />
              <span className="hidden sm:inline">接收目录</span>
            </button>
          </div>
        </header>

        {/* Active Workspace View */}
        <main className="flex-1 min-h-0 overflow-hidden">
          {/* ==============================================================
              VIEW 01: TRANSFER RADAR + SWISS TRANSFER LEDGER (5 : 7 SPLIT)
             ============================================================== */}
          {tab === 'transfer' && (
            <div className="h-full grid grid-cols-1 lg:grid-cols-12 divide-y lg:divide-y-0 lg:divide-x divide-hairline overflow-y-auto lg:overflow-hidden">
              {/* LEFT 5 COLS: Hardware Polar Radar Instrument + Docked QR/Actions */}
              <section className="lg:col-span-5 flex flex-col bg-canvas lg:overflow-y-auto">
                {/* Polar Radar Instrument Stage */}
                <div className="relative flex-1 min-h-[300px] swiss-dot-grid flex flex-col items-center justify-center p-5 overflow-hidden">
                  {/* Corner Telemetry Annotations */}
                  <div className="absolute top-3 left-4 font-mono-tech text-[10px] text-ink-3 tracking-wider">
                    LAN P2P · {data.activeAdapter.ip}
                  </div>
                  <div className="absolute top-3 right-4 font-mono-tech text-[10px] text-ink-3 tracking-wider">
                    {hasDevices ? `LOCKED (${data.devices.length})` : 'STANDBY'}
                  </div>

                  {/* Concentric Polar Coordinate Dial */}
                  <div className="relative w-[228px] h-[228px] sm:w-[248px] sm:h-[248px] flex items-center justify-center">
                    {/* Crosshair Axes */}
                    <div className="absolute inset-x-0 top-1/2 h-px bg-[var(--hairline)] pointer-events-none" />
                    <div className="absolute inset-y-0 left-1/2 w-px bg-[var(--hairline)] pointer-events-none" />

                    {/* Calibrated Concentric Rings */}
                    <div className="absolute inset-0 rounded-full border border-hairline-strong" />
                    <div className="absolute inset-[20%] rounded-full border border-hairline" />
                    <div className="absolute inset-[40%] rounded-full border border-hairline" />

                    {/* Cardinal Degree Ticks */}
                    <span className="absolute -top-4 left-1/2 -translate-x-1/2 font-mono-tech text-[9px] text-ink-3">0°</span>
                    <span className="absolute -bottom-4 left-1/2 -translate-x-1/2 font-mono-tech text-[9px] text-ink-3">180°</span>
                    <span className="absolute -left-6 top-1/2 -translate-y-1/2 font-mono-tech text-[9px] text-ink-3">270°</span>
                    <span className="absolute -right-5 top-1/2 -translate-y-1/2 font-mono-tech text-[9px] text-ink-3">90°</span>

                    {/* Vernier Sweep & Signal Ping */}
                    <div className="vernier-sweep" />
                    <span className="signal-ping" />
                    <span className="signal-ping signal-ping-delay" />

                    {/* CENTER FOCAL ANCHOR — THE SINGLE 5% SIGNAL VERMILION CORE */}
                    <button
                      onClick={pickNativeFiles}
                      disabled={uploadingOutbox}
                      title="点击选择电脑文件（支持多选、大文件零拷贝），或直接拖文件到窗口"
                      className="keycap-btn group relative z-20 w-[76px] h-[76px] rounded-full bg-signal text-white shadow-lg flex flex-col items-center justify-center cursor-pointer ring-4 ring-[var(--canvas)]"
                    >
                      <Send className="w-5 h-5 transition-transform group-hover:-translate-y-0.5 group-hover:translate-x-0.5" />
                      <span className="text-[10px] font-semibold mt-1 tracking-wide">
                        {uploadingOutbox ? '发送中' : '投送文件'}
                      </span>
                    </button>

                    {/* Orbiting Live Peer Chips */}
                    {data.devices.map((dev, idx) => {
                      const angle = -Math.PI / 2 + (2 * Math.PI * idx) / Math.max(1, data.devices.length);
                      const r = 42;
                      const left = 50 + r * Math.cos(angle);
                      const top = 50 + r * Math.sin(angle);
                      return (
                        <button
                          key={dev.id}
                          onClick={pickNativeFiles}
                          style={{ left: `${left}%`, top: `${top}%`, transform: 'translate(-50%, -50%)' }}
                          title={`点击向 ${dev.deviceName} (${dev.ip}) 发送文件`}
                          className="keycap-btn absolute z-30 flex items-center gap-1.5 px-2.5 py-1 rounded-sm bg-elevated border border-hairline-strong hover:border-signal shadow-md cursor-pointer"
                        >
                          <span className="w-1.5 h-1.5 rounded-full bg-signal shrink-0" />
                          <span className="text-[11px] font-medium text-ink-1 whitespace-nowrap max-w-[96px] truncate">
                            {dev.deviceName}
                          </span>
                        </button>
                      );
                    })}
                  </div>

                  <div className="mt-6 text-[11px] text-ink-3 text-center">
                    点击中心按键选择本机文件，或直接将文件拖入窗口任意位置
                  </div>
                </div>

                {/* Docked Bottom Instrument Panel: QR Pairing (if 0 peers) or Quick Controls (if >=1 peer) */}
                <div className="border-t border-hairline bg-surface p-5 shrink-0">
                  {!hasDevices ? (
                    <QRMatrixBlock
                      data={data}
                      qrMode={qrMode}
                      setQrMode={setQrMode}
                      qrBust={qrBust}
                      activeUrl={activeUrl}
                      compact
                      onRefresh={loadBootstrap}
                      onCopyLink={async () => {
                        await copyTextSafe(activeUrl);
                        notify('已复制配对链接');
                      }}
                    />
                  ) : (
                    <div className="space-y-3">
                      <div className="flex items-center justify-between">
                        <span className="text-[10px] font-mono-tech uppercase tracking-wider text-ink-3">
                          QUICK TRANSMIT ACTIONS
                        </span>
                        <button
                          onClick={() => setShowQRModal(true)}
                          className="text-[11px] font-medium text-ink-2 hover:text-signal cursor-pointer"
                        >
                          显示二维码 +
                        </button>
                      </div>
                      <div className="grid grid-cols-2 gap-2">
                        <button
                          onClick={pickNativeFiles}
                          className="keycap-btn py-2.5 px-3 rounded-sm bg-signal text-white text-xs font-semibold flex items-center justify-center gap-1.5 cursor-pointer"
                        >
                          <Upload className="w-3.5 h-3.5" />
                          零拷贝发文件
                        </button>
                        <button
                          onClick={() => pushPCClipboard()}
                          className="keycap-btn py-2.5 px-3 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-xs font-medium flex items-center justify-center gap-1.5 cursor-pointer"
                        >
                          <ClipboardCopy className="w-3.5 h-3.5" />
                          推送剪贴板
                        </button>
                      </div>
                      <div className="text-[11px] text-ink-3 font-mono-tech truncate">
                        SAVE DIR · {data.config.saveDir}
                      </div>
                    </div>
                  )}
                </div>
              </section>

              {/* RIGHT 7 COLS: Precision Swiss Transfer Ledger */}
              <section className="lg:col-span-7 flex flex-col bg-surface lg:overflow-hidden">
                {/* Ledger Filter & Search Bar */}
                <div className="h-[44px] px-4 border-b border-hairline flex items-center justify-between gap-3 shrink-0 bg-surface">
                  <div className="flex items-center h-full gap-4 text-xs">
                    <UnderlineTab active={filterDir === 'all'} onClick={() => setFilterDir('all')}>
                      全部 <span className="font-mono-tech text-[10px] text-ink-3 ml-1">{data.transfers.length}</span>
                    </UnderlineTab>
                    <UnderlineTab active={filterDir === 'in'} onClick={() => setFilterDir('in')}>
                      收到 <span className="font-mono-tech text-[10px] text-ink-3 ml-1">{data.transfers.filter((t) => t.direction === 'in').length}</span>
                    </UnderlineTab>
                    <UnderlineTab active={filterDir === 'out'} onClick={() => setFilterDir('out')}>
                      发出 <span className="font-mono-tech text-[10px] text-ink-3 ml-1">{data.transfers.filter((t) => t.direction === 'out').length}</span>
                    </UnderlineTab>
                  </div>

                  <div className="flex items-center gap-2">
                    <div className="relative w-36 sm:w-48">
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
                        title="明细表"
                        className={`p-1.5 cursor-pointer ${viewMode === 'list' ? 'bg-well text-ink-1' : 'text-ink-3 hover:text-ink-1'}`}
                      >
                        <List className="w-3.5 h-3.5" />
                      </button>
                      <button
                        onClick={() => setViewMode('grid')}
                        title="画廊接触表"
                        className={`p-1.5 border-l border-hairline cursor-pointer ${viewMode === 'grid' ? 'bg-well text-ink-1' : 'text-ink-3 hover:text-ink-1'}`}
                      >
                        <Grid2x2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                    {data.transfers.length > 0 && (
                      <button
                        onClick={async () => {
                          await apiJSON('/api/local/transfers/clear', {
                            method: 'POST',
                            body: JSON.stringify({ direction: filterDir === 'all' ? '' : filterDir }),
                          });
                          notify('已清空传输记录（本地磁盘文件保留）');
                        }}
                        className="text-[11px] text-ink-3 hover:text-signal cursor-pointer pl-1"
                      >
                        清空
                      </button>
                    )}
                  </div>
                </div>

                {/* Ledger Content */}
                <div className="flex-1 overflow-y-auto">
                  {visibleTransfers.length === 0 ? (
                    <div className="h-full min-h-[280px] flex flex-col items-center justify-center p-8 text-center space-y-2">
                      <div className="font-mono-tech text-[11px] uppercase tracking-widest text-ink-3">NO TRANSFERS YET</div>
                      <p className="text-xs text-ink-2">
                        {searchQuery ? '未找到匹配的文件记录' : '在左侧拖入电脑文件，或用 iPhone 扫码 / 快捷指令发送照片与文档'}
                      </p>
                    </div>
                  ) : viewMode === 'grid' ? (
                    <div className="grid grid-cols-2 sm:grid-cols-3 gap-px bg-[var(--hairline)] border-b border-hairline">
                      {visibleTransfers.map((item) => (
                        <div key={item.id} className="group bg-surface flex flex-col">
                          <div
                            onClick={() =>
                              isPreviewableImg(item)
                                ? setQuickLook({
                                    src: fileDownloadUrl(item.id, true),
                                    title: item.name,
                                    subtitle: `${formatBytes(item.size)} · ${item.deviceName} · ${formatTime(item.createdAt)}`,
                                    transferId: item.id,
                                    downloadHref: fileDownloadUrl(item.id, false),
                                  })
                                : apiJSON(`/api/local/open/${encodeURIComponent(item.id)}`, { method: 'POST' }).catch((e) => notify(e.message))
                            }
                            className="aspect-square bg-canvas flex items-center justify-center overflow-hidden relative cursor-pointer"
                          >
                            {isPreviewableImg(item) ? (
                              <img
                                src={fileDownloadUrl(item.id, true)}
                                alt={item.name}
                                className="w-full h-full object-cover group-hover:scale-[1.03] transition-transform duration-200"
                              />
                            ) : (
                              <FileExtStamp name={item.name} large />
                            )}
                            <span className="absolute top-2 left-2 px-1.5 py-0.5 rounded-xs font-mono-tech text-[9px] uppercase bg-surface/90 border border-hairline text-ink-1">
                              {item.direction === 'in' ? 'IN' : 'OUT'}
                            </span>
                          </div>
                          <div className="p-2.5 border-t border-hairline space-y-1">
                            <div className="text-xs font-medium text-ink-1 truncate">{item.name}</div>
                            <div className="flex items-center justify-between font-mono-tech text-[10px] text-ink-3">
                              <span>{formatBytes(item.size)}</span>
                              <div className="flex items-center gap-1">
                                <button
                                  onClick={() => apiJSON(`/api/local/reveal/${encodeURIComponent(item.id)}`, { method: 'POST' }).catch((e) => notify(e.message))}
                                  title="定位"
                                  className="p-1 hover:text-ink-1 cursor-pointer"
                                >
                                  <FolderOpen className="w-3 h-3" />
                                </button>
                                <button
                                  onClick={() => apiJSON(`/api/local/transfer/${encodeURIComponent(item.id)}`, { method: 'DELETE' })}
                                  title="移除"
                                  className="p-1 hover:text-signal cursor-pointer"
                                >
                                  <Trash2 className="w-3 h-3" />
                                </button>
                              </div>
                            </div>
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="divide-y divide-hairline border-b border-hairline">
                      {visibleTransfers.map((item) => (
                        <div
                          key={item.id}
                          className="group px-4 py-3 flex items-center justify-between gap-3 hover:bg-canvas transition"
                        >
                          <div className="flex items-center gap-3 min-w-0">
                            {isPreviewableImg(item) ? (
                              <button
                                onClick={() =>
                                  setQuickLook({
                                    src: fileDownloadUrl(item.id, true),
                                    title: item.name,
                                    subtitle: `${formatBytes(item.size)} · ${item.deviceName} · ${formatTime(item.createdAt)}`,
                                    transferId: item.id,
                                    downloadHref: fileDownloadUrl(item.id, false),
                                  })
                                }
                                title="点击大图预览"
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
                                <span
                                  className={`inline-flex items-center gap-0.5 font-mono-tech text-[10px] px-1.5 py-0.2 rounded-xs border shrink-0 ${
                                    item.direction === 'in'
                                      ? 'border-hairline bg-well text-ink-1'
                                      : 'border-signal/30 bg-signal-soft text-signal'
                                  }`}
                                >
                                  {item.direction === 'in' ? (
                                    <>
                                      <ArrowDownLeft className="w-2.5 h-2.5" /> 收到
                                    </>
                                  ) : (
                                    <>
                                      <ArrowUpRight className="w-2.5 h-2.5" /> 发出
                                    </>
                                  )}
                                </span>
                                <span className="text-[13px] font-medium text-ink-1 truncate">{item.name}</span>
                              </div>
                              <div className="font-mono-tech text-[11px] text-ink-3 mt-0.5 flex items-center gap-2">
                                <span>{formatBytes(item.size)}</span>
                                <span>·</span>
                                <span className="truncate">{item.deviceName}</span>
                                <span>·</span>
                                <span>{formatTime(item.createdAt)}</span>
                              </div>
                            </div>
                          </div>

                          <div className="flex items-center gap-1 shrink-0">
                            <button
                              onClick={() => apiJSON(`/api/local/open/${encodeURIComponent(item.id)}`, { method: 'POST' }).catch((e) => notify(e.message))}
                              title="打开文件"
                              className="keycap-btn px-2 py-1.5 rounded-sm border border-transparent hover:border-hairline hover:bg-elevated text-ink-2 hover:text-ink-1 text-[11px] inline-flex items-center gap-1 cursor-pointer"
                            >
                              <ExternalLink className="w-3.5 h-3.5" />
                              <span className="hidden sm:inline">打开</span>
                            </button>
                            <button
                              onClick={() => apiJSON(`/api/local/reveal/${encodeURIComponent(item.id)}`, { method: 'POST' }).catch((e) => notify(e.message))}
                              title="在资源管理器中定位"
                              className="keycap-btn p-1.5 rounded-sm border border-transparent hover:border-hairline hover:bg-elevated text-ink-2 hover:text-ink-1 cursor-pointer"
                            >
                              <FolderOpen className="w-3.5 h-3.5" />
                            </button>
                            <button
                              onClick={() => apiJSON(`/api/local/transfer/${encodeURIComponent(item.id)}`, { method: 'DELETE' })}
                              title="从列表移除"
                              className="keycap-btn p-1.5 rounded-sm border border-transparent hover:border-hairline hover:bg-elevated text-ink-3 hover:text-signal cursor-pointer"
                            >
                              <Trash2 className="w-3.5 h-3.5" />
                            </button>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </section>
            </div>
          )}

          {/* ==============================================================
              VIEW 02: BIDIRECTIONAL CLIPBOARD (5 : 7 SWISS SPLIT)
             ============================================================== */}
          {tab === 'clipboard' && (
            <div className="h-full grid grid-cols-1 lg:grid-cols-12 divide-y lg:divide-y-0 lg:divide-x divide-hairline overflow-y-auto lg:overflow-hidden">
              {/* Left Composer & Sync Controls */}
              <div className="lg:col-span-5 bg-surface flex flex-col justify-between lg:overflow-y-auto">
                <div className="p-5 space-y-4">
                  <div className="flex items-center justify-between">
                    <div>
                      <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">TRANSMIT CLIPBOARD</div>
                      <h2 className="text-sm font-semibold text-ink-1 mt-0.5">推送文字或截图到手机 / Mac</h2>
                    </div>
                    <kbd className="font-mono-tech text-[10px] px-1.5 py-0.5 rounded-xs border border-hairline bg-canvas text-ink-3">
                      Ctrl+V
                    </kbd>
                  </div>

                  <textarea
                    rows={5}
                    value={clipInput}
                    onChange={(e) => setClipInput(e.target.value)}
                    placeholder="输入要同步的文本、链接或验证码；或直接点下方按钮读取当前 Windows 剪贴板（含 Win+Shift+S 截图）..."
                    className="w-full rounded-sm bg-canvas border border-hairline p-3 text-xs text-ink-1 placeholder:text-ink-3 leading-relaxed focus:outline-none focus:border-signal resize-none select-text"
                  />

                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                    <button
                      onClick={() => pushPCClipboard(clipInput)}
                      disabled={!clipInput.trim()}
                      className="keycap-btn py-2.5 px-3 rounded-sm bg-signal disabled:opacity-35 text-white text-xs font-semibold cursor-pointer"
                    >
                      发送文本框内容
                    </button>
                    <button
                      onClick={() => pushPCClipboard()}
                      className="keycap-btn py-2.5 px-3 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-xs font-medium cursor-pointer"
                    >
                      读取电脑剪贴板并推送
                    </button>
                  </div>
                </div>

                <div className="border-t border-hairline p-5 space-y-4 bg-canvas">
                  <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">SYNC RULES</div>
                  <SpecToggle
                    title="收到后自动写入 Windows 剪贴板"
                    desc="手机/Mac 发来文字或图片时直接写入系统剪贴板，可立刻 Ctrl+V"
                    checked={data.config.autoCopyText}
                    onChange={(v) => updateConfig({ autoCopyText: v })}
                  />
                  <SpecToggle
                    title="自动监听电脑 Ctrl+C 并推送"
                    desc="开启后，在 Windows 上复制文字或截屏会自动同步到在线手机/Mac"
                    checked={data.config.clipboardAutoPush}
                    onChange={(v) => updateConfig({ clipboardAutoPush: v })}
                  />
                </div>
              </div>

              {/* Right Clipboard History Ledger */}
              <div className="lg:col-span-7 bg-surface flex flex-col lg:overflow-hidden">
                <div className="h-[44px] px-4 border-b border-hairline flex items-center justify-between shrink-0">
                  <span className="text-xs font-semibold text-ink-1">
                    同步历史 <span className="font-mono-tech text-ink-3 ml-1">{data.clipboard.length}</span>
                  </span>
                  {data.clipboard.length > 0 && (
                    <button
                      onClick={async () => {
                        await apiJSON('/api/local/clipboard/clear', { method: 'POST' });
                        notify('已清空剪贴板历史');
                      }}
                      className="text-[11px] text-ink-3 hover:text-signal cursor-pointer"
                    >
                      清空全部
                    </button>
                  )}
                </div>

                <div className="flex-1 overflow-y-auto divide-y divide-hairline">
                  {data.clipboard.length === 0 ? (
                    <div className="h-full min-h-[260px] flex flex-col items-center justify-center p-8 text-center space-y-1.5">
                      <div className="font-mono-tech text-[11px] uppercase tracking-widest text-ink-3">CLIPBOARD EMPTY</div>
                      <p className="text-xs text-ink-2">在窗口任意位置按 Ctrl+V 即可推送当前文字或截图</p>
                    </div>
                  ) : (
                    data.clipboard.map((c) => (
                      <div key={c.id} className="p-4 space-y-2.5 hover:bg-canvas transition">
                        <div className="flex items-center justify-between gap-2 text-[11px]">
                          <div className="flex items-center gap-2 font-mono-tech">
                            <span
                              className={`px-1.5 py-0.2 rounded-xs border text-[10px] ${
                                c.source === 'pc'
                                  ? 'border-hairline bg-well text-ink-1'
                                  : 'border-signal/30 bg-signal-soft text-signal'
                              }`}
                            >
                              {c.deviceName}
                            </span>
                            <span className="text-ink-3">{formatTime(c.createdAt)}</span>
                          </div>
                          <div className="flex items-center gap-1.5">
                            <button
                              onClick={() => copyClipboardToPC(c.id)}
                              className="keycap-btn inline-flex items-center gap-1 px-2.5 py-1 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-[11px] font-medium cursor-pointer"
                            >
                              <Copy className="w-3 h-3" />
                              复制到电脑
                            </button>
                            <button
                              onClick={() => apiJSON(`/api/local/clipboard/${encodeURIComponent(c.id)}`, { method: 'DELETE' })}
                              className="p-1 rounded-sm hover:bg-well text-ink-3 hover:text-signal cursor-pointer"
                            >
                              <X className="w-3.5 h-3.5" />
                            </button>
                          </div>
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
                            <img src={clipImageUrl(c.id)} alt="剪贴板截图" className="max-h-48 object-contain" />
                          </button>
                        ) : (
                          <div className="space-y-1 select-text">
                            <p className="text-[13px] text-ink-1 whitespace-pre-wrap break-all leading-relaxed line-clamp-5">
                              {safePreviewText(c.text)}
                            </p>
                            {(c.text?.length || 0) > 500 && (
                              <span className="inline-block font-mono-tech text-[10px] text-ink-3">
                                TRUNCATED · {c.text!.length.toLocaleString()} CHARS（点击复制完整内容）
                              </span>
                            )}
                          </div>
                        )}
                      </div>
                    ))
                  )}
                </div>
              </div>
            </div>
          )}

          {/* ==============================================================
              VIEW 03: APPLE SHORTCUTS BLUEPRINT + LIVE TERMINAL
             ============================================================== */}
          {tab === 'shortcuts' && (
            <ShortcutGuide
              baseUrl={data.baseUrl}
              mdnsHost={data.mdnsHost}
              port={data.port}
              token={data.config.token}
              logs={data.shortcutLogs}
              onNotify={notify}
            />
          )}

          {/* ==============================================================
              VIEW 04: SYSTEM & NETWORK SPEC SHEET (6 : 6 SWISS SPLIT)
             ============================================================== */}
          {tab === 'settings' && (
            <div className="h-full grid grid-cols-1 lg:grid-cols-12 divide-y lg:divide-y-0 lg:divide-x divide-hairline overflow-y-auto lg:overflow-hidden">
              {/* Left 6 Cols: Identity, Storage, Network Adapter */}
              <div className="lg:col-span-6 bg-surface divide-y divide-hairline lg:overflow-y-auto">
                <div className="p-5 space-y-4">
                  <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">DEVICE & STORAGE</div>

                  <div className="space-y-1.5">
                    <label className="text-xs font-medium text-ink-2">本机显示名称（同时更新 mDNS .local 域名）</label>
                    <div className="flex gap-2">
                      <input
                        value={deviceNameDraft}
                        onChange={(e) => setDeviceNameDraft(e.target.value)}
                        className="flex-1 px-3 py-2 rounded-sm bg-canvas border border-hairline text-xs text-ink-1 font-mono-tech focus:outline-none focus:border-signal select-text"
                      />
                      <button
                        disabled={savingName || !deviceNameDraft.trim()}
                        onClick={async () => {
                          setSavingName(true);
                          await updateConfig({ deviceName: deviceNameDraft.trim() });
                          setSavingName(false);
                        }}
                        className="keycap-btn px-4 py-2 rounded-sm bg-signal text-white text-xs font-semibold cursor-pointer"
                      >
                        保存
                      </button>
                    </div>
                  </div>

                  <div className="space-y-1.5">
                    <label className="text-xs font-medium text-ink-2">接收文件保存目录</label>
                    <div className="flex gap-2">
                      <input
                        readOnly
                        value={data.config.saveDir}
                        className="flex-1 px-3 py-2 rounded-sm bg-canvas border border-hairline text-xs font-mono-tech text-ink-2 select-text"
                      />
                      <button
                        onClick={pickFolder}
                        className="keycap-btn px-3.5 py-2 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-1 text-xs font-medium shrink-0 cursor-pointer"
                      >
                        更改目录
                      </button>
                    </div>
                  </div>

                  <div className="space-y-1.5">
                    <label className="text-xs font-medium text-ink-2">局域网广播网卡（已自动过滤代理/虚拟网卡）</label>
                    <select
                      value={data.config.selectedInterface}
                      onChange={(e) => updateConfig({ selectedInterface: e.target.value })}
                      className="w-full px-3 py-2 rounded-sm bg-canvas border border-hairline text-xs font-mono-tech text-ink-1 focus:outline-none focus:border-signal"
                    >
                      <option value="">
                        AUTO · {data.activeAdapter.name} ({data.activeAdapter.ip})
                      </option>
                      {data.adapters.map((a) => (
                        <option key={`${a.name}-${a.ip}`} value={a.name}>
                          {a.name} · {a.ip} {a.recommended ? '[RECOMMENDED]' : a.isVirtual ? '[VIRTUAL]' : ''}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>

                <div className="p-5 space-y-4">
                  <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">RECEIVE BEHAVIOR</div>
                  <SpecToggle
                    title="接收前弹窗询问确认（原生 AirDrop 机制）"
                    desc="开启后，每次收到文件先在桌面弹出「接收/拒绝」确认框；关闭则已配对设备直接落盘并弹通知"
                    checked={data.config.confirmReceive}
                    onChange={(v) => updateConfig({ confirmReceive: v })}
                  />
                  <SpecToggle
                    title="Windows 11 原生系统通知"
                    desc="收到文件后弹出系统通知，点击可直接打开文件或定位所在目录"
                    checked={data.config.notifyOnReceive}
                    onChange={(v) => updateConfig({ notifyOnReceive: v })}
                  />
                </div>
              </div>

              {/* Right 6 Cols: Windows Integration & 256-bit Security */}
              <div className="lg:col-span-6 bg-surface divide-y divide-hairline lg:overflow-y-auto">
                <div className="p-5 space-y-4">
                  <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">WINDOWS INTEGRATION</div>
                  <SpecToggle
                    title="开机自动启动（静默驻留托盘）"
                    desc="无需管理员权限，登录后后台安静监听，不自动弹出打扰窗口"
                    checked={data.autostart}
                    onChange={toggleAutostart}
                  />
                  <SpecToggle
                    title="资源管理器右键「发送到 → 隔空传」"
                    desc="右键任意电脑文件即可零拷贝推送到手机 / Mac 队列"
                    checked={data.sendTo}
                    onChange={toggleSendTo}
                  />
                </div>

                <div className="p-5 space-y-3">
                  <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">FIREWALL ACCESS</div>
                  <div className="border border-hairline rounded-sm bg-canvas p-4 flex items-center justify-between gap-3">
                    <div className="flex items-center gap-3">
                      {data.firewallOk ? (
                        <ShieldCheck className="w-4 h-4 text-[var(--ok)] shrink-0" />
                      ) : (
                        <ShieldAlert className="w-4 h-4 text-signal shrink-0" />
                      )}
                      <div>
                        <div className="text-xs font-semibold text-ink-1">
                          Windows Defender 防火墙：{data.firewallOk ? '已放行' : '未放行'}
                        </div>
                        <div className="font-mono-tech text-[11px] text-ink-3 mt-0.5">
                          ALLOW INBOUND TCP {data.port} (PRIVATE + PUBLIC)
                        </div>
                      </div>
                    </div>
                    <button
                      onClick={allowFirewall}
                      className="keycap-btn px-3 py-1.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-xs font-medium text-ink-1 shrink-0 cursor-pointer"
                    >
                      {data.firewallOk ? '重新配置' : '一键放行'}
                    </button>
                  </div>
                </div>

                <div className="p-5 space-y-3">
                  <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">SECURITY TOKEN (256-BIT)</div>
                  <div className="border border-hairline rounded-sm bg-canvas p-3.5 space-y-3">
                    <div className="font-mono-tech text-[11px] text-ink-1 break-all select-text">{data.config.token}</div>
                    <button
                      onClick={resetToken}
                      className="keycap-btn px-3.5 py-1.5 rounded-sm border border-signal/40 bg-signal-soft hover:bg-signal text-signal hover:text-white text-xs font-semibold cursor-pointer"
                    >
                      重置配对令牌并断开所有旧设备
                    </button>
                  </div>
                </div>
              </div>
            </div>
          )}
        </main>
      </div>
    </div>
  );
}

/* ============================================================================
   SUB-COMPONENTS
   ============================================================================ */

function RailNavItem({
  idx,
  label,
  count,
  active,
  icon,
  onClick,
}: {
  idx: string;
  label: string;
  count?: number;
  active: boolean;
  icon: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={`w-full px-4 py-2.5 flex items-center justify-between text-xs relative transition cursor-pointer ${
        active ? 'bg-canvas text-ink-1 font-semibold' : 'text-ink-2 hover:bg-well/45 hover:text-ink-1'
      }`}
    >
      {active && <span className="absolute left-0 top-1.5 bottom-1.5 w-[2.5px] bg-signal" />}
      <div className="flex items-center gap-2.5">
        <span className={`font-mono-tech text-[10px] ${active ? 'text-signal' : 'text-ink-3'}`}>{idx}</span>
        <span className={active ? 'text-ink-1' : 'text-ink-3'}>{icon}</span>
        <span>{label}</span>
      </div>
      {count !== undefined && (
        <span className="font-mono-tech text-[10px] text-ink-3">{count}</span>
      )}
    </button>
  );
}

function UnderlineTab({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      className={`h-full inline-flex items-center border-b-2 transition cursor-pointer ${
        active
          ? 'border-signal text-ink-1 font-semibold'
          : 'border-transparent text-ink-2 hover:text-ink-1'
      }`}
    >
      {children}
    </button>
  );
}

function QRMatrixBlock({
  data,
  qrMode,
  setQrMode,
  qrBust,
  activeUrl,
  compact = false,
  onRefresh,
  onCopyLink,
}: {
  data: LocalBootstrap;
  qrMode: 'ip' | 'mdns';
  setQrMode: (m: 'ip' | 'mdns') => void;
  qrBust: number;
  activeUrl: string;
  compact?: boolean;
  onRefresh: () => void;
  onCopyLink: () => void;
}) {
  return (
    <div className={compact ? 'flex items-center gap-4' : 'space-y-4'}>
      <div className="qr-bracket p-2 bg-white border border-hairline shrink-0 mx-auto">
        <img
          src={`/api/local/qr.png?mode=${qrMode}&v=${qrBust}`}
          alt="扫码配对二维码"
          className={compact ? 'w-28 h-28 block' : 'w-44 h-44 block'}
        />
      </div>

      <div className="flex-1 min-w-0 space-y-2.5">
        <div>
          <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">SCAN TO PAIR</div>
          <div className="text-xs font-semibold text-ink-1 mt-0.5">iPhone 相机扫一次即可免装互传</div>
        </div>

        {data.mdnsPairUrl && (
          <div className="inline-flex border border-hairline rounded-sm overflow-hidden text-[10px] font-mono-tech">
            <button
              onClick={() => setQrMode('ip')}
              className={`px-2 py-0.5 cursor-pointer ${qrMode === 'ip' ? 'bg-well text-ink-1 font-semibold' : 'text-ink-3'}`}
            >
              IP
            </button>
            <button
              onClick={() => setQrMode('mdns')}
              className={`px-2 py-0.5 border-l border-hairline cursor-pointer ${qrMode === 'mdns' ? 'bg-well text-ink-1 font-semibold' : 'text-ink-3'}`}
            >
              .local
            </button>
          </div>
        )}

        <div className="flex items-center gap-1.5">
          <button
            onClick={onCopyLink}
            title={activeUrl}
            className="keycap-btn flex-1 inline-flex items-center justify-center gap-1 py-1.5 px-2.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-[11px] font-medium text-ink-1 cursor-pointer truncate"
          >
            <Copy className="w-3 h-3 shrink-0" />
            <span className="truncate">复制链接</span>
          </button>
          <button
            onClick={onRefresh}
            title="刷新二维码"
            className="keycap-btn p-1.5 rounded-sm border border-hairline bg-elevated hover:bg-well text-ink-2 cursor-pointer"
          >
            <RefreshCw className="w-3 h-3" />
          </button>
        </div>
      </div>
    </div>
  );
}

function SpecToggle({
  title,
  desc,
  checked,
  onChange,
}: {
  title: string;
  desc: string;
  checked: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="space-y-0.5">
        <div className="text-xs font-medium text-ink-1">{title}</div>
        <div className="text-[11px] text-ink-3 leading-relaxed">{desc}</div>
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative inline-flex h-5 w-9 shrink-0 rounded-sm border transition-colors cursor-pointer ${
          checked ? 'bg-signal border-signal' : 'bg-well border-hairline-strong'
        }`}
      >
        <span
          className={`inline-block h-3.5 w-3.5 rounded-[2px] bg-white shadow-xs transition-transform mt-[2px] ${
            checked ? 'translate-x-[17px]' : 'translate-x-[2px]'
          }`}
        />
      </button>
    </div>
  );
}

function PlatformIcon({ platform, small = false }: { platform: ClientInfo['platform']; small?: boolean }) {
  const cls = small ? 'w-3.5 h-3.5' : 'w-4 h-4';
  switch (platform) {
    case 'ios':
    case 'android':
      return <Smartphone className={cls} />;
    case 'ipad':
      return <Tablet className={cls} />;
    case 'macos':
      return <Laptop className={cls} />;
    default:
      return <Monitor className={cls} />;
  }
}

export function safePreviewText(text?: string, limit = 500): string {
  if (!text) return '';
  return text.length > limit ? text.slice(0, limit) + '…' : text;
}

export function isPreviewableImg(item: TransferItem): boolean {
  if (item.kind !== 'image') return false;
  const lower = (item.name || '').toLowerCase();
  const mime = (item.mimeType || '').toLowerCase();
  if (lower.endsWith('.heic') || lower.endsWith('.heif') || mime.includes('heic') || mime.includes('heif')) {
    return false;
  }
  return true;
}

export function FileExtStamp({ name, large = false }: { name: string; large?: boolean }) {
  const parts = (name || '').split('.');
  const ext = parts.length > 1 ? parts[parts.length - 1].slice(0, 4).toUpperCase() : 'FILE';
  return (
    <span
      className={`font-mono-tech font-bold tracking-wider text-ink-2 ${
        large ? 'text-sm px-2.5 py-1 border border-hairline bg-surface rounded-xs' : 'text-[10px]'
      }`}
    >
      {ext}
    </span>
  );
}
