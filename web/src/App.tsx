import React, { useEffect, useState } from 'react';
import { KeyRound, Loader2, Moon, Radio, Sun } from 'lucide-react';
import { apiJSON, getStoredToken, PingResp, setStoredToken } from './api';
import { useTheme } from './utils/sound';
import PCDashboard from './components/PCDashboard';
import MobileClient from './components/MobileClient';

export default function App() {
  const [loading, setLoading] = useState(true);
  const [ping, setPing] = useState<PingResp | null>(null);
  const [manualToken, setManualToken] = useState('');
  const [pairErr, setPairErr] = useState('');
  const [pairing, setPairing] = useState(false);
  const theme = useTheme();

  const forceMobile = new URLSearchParams(window.location.search).get('view') === 'mobile';

  async function init() {
    setLoading(true);
    try {
      const params = new URLSearchParams(window.location.search);
      const urlToken = params.get('t') || params.get('token');
      if (urlToken) {
        setStoredToken(urlToken);
        try {
          await apiJSON('/api/pair', {
            method: 'POST',
            body: JSON.stringify({ token: urlToken }),
          });
        } catch {
          // handled via ping below
        }
        params.delete('t');
        params.delete('token');
        const nextSearch = params.toString();
        window.history.replaceState({}, '', `${window.location.pathname}${nextSearch ? '?' + nextSearch : ''}`);
      } else {
        const saved = getStoredToken();
        if (saved) {
          await apiJSON('/api/pair', {
            method: 'POST',
            body: JSON.stringify({ token: saved }),
          }).catch(() => {});
        }
      }

      const res = await apiJSON<PingResp>('/api/ping');
      setPing(res);
    } catch {
      setPing(null);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    init();
  }, []);

  async function handleManualPair(e: React.FormEvent) {
    e.preventDefault();
    const tok = manualToken.trim();
    if (!tok) return;
    setPairing(true);
    setPairErr('');
    try {
      await apiJSON('/api/pair', {
        method: 'POST',
        body: JSON.stringify({ token: tok }),
      });
      setStoredToken(tok);
      await init();
    } catch (err) {
      setPairErr(err instanceof Error ? err.message : '配对失败');
    } finally {
      setPairing(false);
    }
  }

  if (loading) {
    return (
      <div className="h-screen bg-canvas flex flex-col items-center justify-center gap-3 text-ink-2">
        <Loader2 className="w-5 h-5 animate-spin text-signal" />
        <p className="text-xs font-mono-tech uppercase tracking-widest">CONNECTING · AIRDROPWIN</p>
      </div>
    );
  }

  if (!ping) {
    return (
      <div className="min-h-screen bg-canvas swiss-dot-grid flex items-center justify-center p-6">
        <div className="bg-surface border border-hairline-strong rounded-md p-7 max-w-md w-full space-y-5 animate-sheet">
          <div className="flex items-center gap-3 border-b border-hairline pb-4">
            <div className="w-9 h-9 rounded-sm bg-well border border-hairline flex items-center justify-center text-signal">
              <Radio className="w-4 h-4" />
            </div>
            <div>
              <div className="text-[11px] font-mono-tech uppercase tracking-wider text-ink-3">OFFLINE</div>
              <h1 className="text-base font-semibold text-ink-1">无法连接到 Windows 主机</h1>
            </div>
          </div>
          <p className="text-xs text-ink-2 leading-relaxed">
            请确认手机 / Mac 与 Windows 电脑在同一 Wi-Fi 或个人热点下，且电脑端已启动 AirDropWin 并放行防火墙端口。
          </p>
          <button
            onClick={init}
            className="keycap-btn w-full py-2.5 rounded-sm bg-signal text-white font-semibold text-xs cursor-pointer"
          >
            重新探测连接
          </button>
        </div>
      </div>
    );
  }

  if (ping.isLocal && !forceMobile) {
    return <PCDashboard isDark={theme.isDark} onToggleTheme={theme.toggle} />;
  }

  if (!ping.authorized) {
    return (
      <div className="min-h-screen bg-canvas swiss-dot-grid flex items-center justify-center p-5 relative">
        <button
          onClick={theme.toggle}
          className="keycap-btn fixed top-4 right-4 p-2 rounded-sm bg-surface border border-hairline text-ink-2 cursor-pointer"
          aria-label="切换主题"
        >
          {theme.isDark ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
        </button>

        <div className="bg-surface border border-hairline-strong rounded-md p-6 max-w-sm w-full space-y-5 animate-sheet">
          <div className="flex items-center gap-3 border-b border-hairline pb-4">
            <div className="w-9 h-9 rounded-sm bg-well border border-hairline flex items-center justify-center text-signal shrink-0">
              <KeyRound className="w-4 h-4" />
            </div>
            <div className="min-w-0">
              <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">PAIRING REQUIRED</div>
              <h1 className="text-base font-semibold text-ink-1 truncate">配对至 {ping.deviceName}</h1>
            </div>
          </div>

          <p className="text-xs text-ink-2 leading-relaxed">
            用 iPhone 相机直接扫描电脑屏幕二维码即可免密接入，或在下方粘贴 256-bit 配对令牌。
          </p>

          <form onSubmit={handleManualPair} className="space-y-3">
            <input
              type="text"
              value={manualToken}
              onChange={(e) => setManualToken(e.target.value)}
              placeholder="粘贴配对令牌 (Token)"
              className="w-full px-3 py-2.5 rounded-sm bg-elevated border border-hairline text-xs text-ink-1 font-mono-tech focus:outline-none focus:border-signal"
            />
            {pairErr && <p className="text-xs text-signal">{pairErr}</p>}
            <button
              type="submit"
              disabled={pairing || !manualToken.trim()}
              className="keycap-btn w-full py-2.5 rounded-sm bg-signal disabled:opacity-40 text-white font-semibold text-xs cursor-pointer"
            >
              {pairing ? '正在校验令牌...' : '确认配对'}
            </button>
          </form>
        </div>
      </div>
    );
  }

  return (
    <MobileClient
      pcName={ping.deviceName}
      isDark={theme.isDark}
      onToggleTheme={theme.toggle}
      onTokenInvalid={() => {
        setStoredToken('');
        init();
      }}
    />
  );
}
