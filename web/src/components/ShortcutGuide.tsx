import React, { useState } from 'react';
import { Check, Copy, Play, Terminal } from 'lucide-react';
import { apiJSON, copyTextSafe, formatTime, ShortcutLog } from '../api';

interface Props {
  baseUrl: string;
  mdnsHost: string;
  port: number;
  token: string;
  logs: ShortcutLog[];
  onNotify: (msg: string) => void;
}

export default function ShortcutGuide({ baseUrl, mdnsHost, port, token, logs, onNotify }: Props) {
  const [useMdns, setUseMdns] = useState(false);
  const [activeRecipe, setActiveRecipe] = useState<'file' | 'pushClip' | 'pullClip'>('file');
  const [copiedKey, setCopiedKey] = useState('');
  const [testing, setTesting] = useState(false);

  const origin = useMdns && mdnsHost ? `http://${mdnsHost}:${port}` : baseUrl;
  const uploadUrl = `${origin}/api/shortcut/upload?token=${token}`;
  const clipUrl = `${origin}/api/shortcut/clipboard?token=${token}`;

  async function copyVal(key: string, val: string) {
    await copyTextSafe(val);
    setCopiedKey(key);
    onNotify('已复制到剪贴板');
    setTimeout(() => setCopiedKey(''), 1800);
  }

  async function testShortcutEndpoint() {
    setTesting(true);
    try {
      const res = await apiJSON<{ message: string }>(`/api/shortcut/ping?token=${encodeURIComponent(token)}`);
      onNotify(res.message || '快捷指令接口自测通过');
    } catch (e) {
      onNotify(e instanceof Error ? e.message : '测试失败');
    } finally {
      setTesting(false);
    }
  }

  return (
    <div className="h-full grid grid-cols-1 lg:grid-cols-12 divide-y lg:divide-y-0 lg:divide-x divide-hairline overflow-y-auto lg:overflow-hidden">
      {/* Left 7 Cols: Endpoint Config + Step-by-Step Visual Blueprint */}
      <div className="lg:col-span-7 flex flex-col lg:overflow-y-auto bg-surface">
        {/* Top Endpoint Strip */}
        <div className="p-5 border-b border-hairline space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="text-[10px] font-mono-tech uppercase tracking-widest text-ink-3">APPLE SHORTCUTS ENDPOINTS</div>
              <h2 className="text-sm font-semibold text-ink-1 mt-0.5">iOS / macOS 系统分享菜单直传接口</h2>
            </div>
            <div className="flex items-center gap-2">
              {mdnsHost && (
                <div className="inline-flex border border-hairline rounded-sm overflow-hidden text-[11px] font-mono-tech">
                  <button
                    onClick={() => setUseMdns(false)}
                    className={`px-2.5 py-1 cursor-pointer transition ${
                      !useMdns ? 'bg-well text-ink-1 font-semibold' : 'bg-surface text-ink-3 hover:text-ink-1'
                    }`}
                  >
                    IP
                  </button>
                  <button
                    onClick={() => setUseMdns(true)}
                    className={`px-2.5 py-1 border-l border-hairline cursor-pointer transition ${
                      useMdns ? 'bg-well text-ink-1 font-semibold' : 'bg-surface text-ink-3 hover:text-ink-1'
                    }`}
                  >
                    .local
                  </button>
                </div>
              )}
              <button
                onClick={testShortcutEndpoint}
                disabled={testing}
                className="keycap-btn inline-flex items-center gap-1.5 px-3 py-1.5 rounded-sm bg-signal text-white text-xs font-medium cursor-pointer"
              >
                <Play className="w-3 h-3" />
                {testing ? '测试中...' : '接口自测'}
              </button>
            </div>
          </div>

          <div className="space-y-2.5">
            <EndpointRow
              index="01"
              method="POST"
              label="发送照片 / 视频 / Office / ZIP（自动识别真实后缀）"
              url={uploadUrl}
              copied={copiedKey === 'upload'}
              onCopy={() => copyVal('upload', uploadUrl)}
            />
            <EndpointRow
              index="02"
              method="GET / POST"
              label="读写剪贴板（发图片/文件也会自动转存不乱码）"
              url={clipUrl}
              copied={copiedKey === 'clip'}
              onCopy={() => copyVal('clip', clipUrl)}
            />
          </div>
        </div>

        {/* Visual Blueprint Selector */}
        <div className="flex border-b border-hairline bg-canvas text-xs">
          <RecipeTab active={activeRecipe === 'file'} onClick={() => setActiveRecipe('file')} num="A" label="相册/文件分享直传" />
          <RecipeTab active={activeRecipe === 'pushClip'} onClick={() => setActiveRecipe('pushClip')} num="B" label="发剪贴板给电脑" />
          <RecipeTab active={activeRecipe === 'pullClip'} onClick={() => setActiveRecipe('pullClip')} num="C" label="取电脑剪贴板" />
        </div>

        {/* Visual Recipe Steps */}
        <div className="p-5 space-y-4 flex-1">
          {activeRecipe === 'file' && (
            <>
              <div className="text-xs text-ink-2 leading-relaxed">
                在 iPhone「快捷指令」新建并命名为 <strong className="text-ink-1">发送到 Windows</strong>，点底部 <strong>ⓘ</strong> 打开「在共享表单中显示」，按以下 4 行积木拼装（Header 留空即可，后端自动深度识别 <code className="font-mono-tech text-[11px] bg-well px-1 rounded-xs">.docx/.xlsx/.heic/.mov/.zip</code>）：
              </div>
              <div className="border border-hairline rounded-sm divide-y divide-hairline bg-elevated font-mono-tech text-xs">
                <BlockLine step="01" text="接收来自 [共享表单] 的 [任意] 输入" />
                <BlockLine step="02" text="对 [快捷指令输入] 中的每一项重复" />
                <div className="px-4 py-3 space-y-1.5 bg-well/40">
                  <div className="flex items-center gap-3">
                    <span className="text-[10px] text-ink-3">03</span>
                    <span className="text-ink-1 font-semibold pl-3">↳ 获取 [上方 01 接口链接] 的内容</span>
                  </div>
                  <div className="pl-11 grid grid-cols-1 sm:grid-cols-3 gap-2 pt-1 text-[11px]">
                    <div className="px-2 py-1 bg-surface border border-hairline rounded-xs">
                      <span className="text-ink-3">方法：</span>
                      <span className="text-signal font-semibold">POST</span>
                    </div>
                    <div className="px-2 py-1 bg-surface border border-hairline rounded-xs">
                      <span className="text-ink-3">请求体：</span>
                      <span className="text-signal font-semibold">文件</span>
                    </div>
                    <div className="px-2 py-1 bg-surface border border-hairline rounded-xs">
                      <span className="text-ink-3">文件：</span>
                      <span className="text-ink-1 font-semibold">重复项目</span>
                    </div>
                  </div>
                </div>
                <BlockLine step="04" text="结束重复" />
                <BlockLine step="05" text="显示通知 [已发送到 Windows]" />
              </div>
              <p className="text-[11px] text-ink-3">
                可选保留原文件名：在「获取 URL 内容 → 头部」加一行键 <code className="font-mono-tech bg-well px-1">X-Filename</code>，值选「重复项目 → 名称」。
              </p>
            </>
          )}

          {activeRecipe === 'pushClip' && (
            <>
              <div className="text-xs text-ink-2 leading-relaxed">
                将 iPhone 剪贴板文字或截图一键推给 Windows，电脑收到后可直接 <code className="font-mono-tech bg-well px-1">Ctrl+V</code>：
              </div>
              <div className="border border-hairline rounded-sm divide-y divide-hairline bg-elevated font-mono-tech text-xs">
                <BlockLine step="01" text="获取剪贴板" />
                <div className="px-4 py-3 space-y-1.5 bg-well/40">
                  <div className="flex items-center gap-3">
                    <span className="text-[10px] text-ink-3">02</span>
                    <span className="text-ink-1 font-semibold">获取 [上方 02 剪贴板链接] 的内容</span>
                  </div>
                  <div className="pl-8 grid grid-cols-3 gap-2 pt-1 text-[11px]">
                    <div className="px-2 py-1 bg-surface border border-hairline rounded-xs">
                      <span className="text-ink-3">方法：</span>
                      <span className="text-signal font-semibold">POST</span>
                    </div>
                    <div className="px-2 py-1 bg-surface border border-hairline rounded-xs">
                      <span className="text-ink-3">请求体：</span>
                      <span className="text-signal font-semibold">文件</span>
                    </div>
                    <div className="px-2 py-1 bg-surface border border-hairline rounded-xs">
                      <span className="text-ink-3">文件：</span>
                      <span className="text-ink-1 font-semibold">剪贴板</span>
                    </div>
                  </div>
                </div>
                <BlockLine step="03" text="显示通知 [已同步到电脑剪贴板]" />
              </div>
            </>
          )}

          {activeRecipe === 'pullClip' && (
            <>
              <div className="text-xs text-ink-2 leading-relaxed">
                仅需 <strong className="text-ink-1">2 个积木</strong>：后端直接返回纯 UTF-8 文本，彻底告别苹果快捷指令 JSON 词典解析报错。
              </div>
              <div className="border border-hairline rounded-sm divide-y divide-hairline bg-elevated font-mono-tech text-xs">
                <BlockLine step="01" text="获取 [上方 02 剪贴板链接] 的内容（默认 GET）" />
                <BlockLine step="02" text="拷贝 [URL 的内容] 至剪贴板" />
              </div>
            </>
          )}
        </div>
      </div>

      {/* Right 5 Cols: Live Telemetry / Diagnostic Log Terminal */}
      <div className="lg:col-span-5 flex flex-col bg-canvas lg:overflow-hidden">
        <div className="px-4 py-3 border-b border-hairline flex items-center justify-between bg-surface">
          <div className="flex items-center gap-2">
            <Terminal className="w-3.5 h-3.5 text-ink-2" />
            <span className="text-xs font-semibold text-ink-1">实时调用诊断流</span>
          </div>
          <span className="text-[10px] font-mono-tech uppercase text-ink-3">LIVE STREAM · {logs.length}</span>
        </div>

        <div className="flex-1 overflow-y-auto divide-y divide-hairline font-mono-tech">
          {logs.length === 0 ? (
            <div className="p-8 text-center space-y-1.5">
              <div className="text-xs text-ink-2">等待快捷指令请求...</div>
              <div className="text-[11px] text-ink-3">点击左上角「接口自测」或用 iPhone 触发一次即可查看详细日志</div>
            </div>
          ) : (
            logs.map((log) => (
              <div key={log.id} className="px-4 py-3 space-y-1 hover:bg-surface transition">
                <div className="flex items-center justify-between gap-2 text-[11px]">
                  <div className="flex items-center gap-2 min-w-0">
                    <span
                      className={`px-1.5 py-0.2 rounded-xs text-[10px] font-bold ${
                        log.status < 300
                          ? 'bg-[var(--ok-soft)] text-[var(--ok)]'
                          : 'bg-signal-soft text-signal'
                      }`}
                    >
                      {log.status}
                    </span>
                    <span className="text-ink-2 truncate">
                      {log.method} {log.path}
                    </span>
                  </div>
                  <span className="text-[10px] text-ink-3 shrink-0">{formatTime(log.createdAt)}</span>
                </div>
                <div className="text-xs text-ink-1 break-all">{log.message}</div>
                <div className="text-[10px] text-ink-3">SRC {log.remoteIp}</div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

function EndpointRow({
  index,
  method,
  label,
  url,
  copied,
  onCopy,
}: {
  index: string;
  method: string;
  label: string;
  url: string;
  copied: boolean;
  onCopy: () => void;
}) {
  return (
    <div className="border border-hairline rounded-sm bg-elevated p-3 space-y-1.5">
      <div className="flex items-center justify-between gap-2 text-[11px]">
        <div className="flex items-center gap-2 min-w-0">
          <span className="font-mono-tech text-ink-3">{index}</span>
          <span className="font-mono-tech px-1.5 py-0.2 bg-well text-ink-1 rounded-xs text-[10px] font-semibold">{method}</span>
          <span className="text-ink-2 truncate">{label}</span>
        </div>
      </div>
      <div className="flex items-center gap-2">
        <code className="flex-1 font-mono-tech text-[11px] text-ink-1 bg-canvas border border-hairline px-2.5 py-1.5 rounded-xs truncate select-all">
          {url}
        </code>
        <button
          onClick={onCopy}
          className="keycap-btn inline-flex items-center gap-1 px-2.5 py-1.5 rounded-xs border border-hairline bg-surface hover:bg-well text-xs font-medium text-ink-1 shrink-0 cursor-pointer"
        >
          {copied ? <Check className="w-3.5 h-3.5 text-[var(--ok)]" /> : <Copy className="w-3.5 h-3.5" />}
          {copied ? '已复制' : '复制'}
        </button>
      </div>
    </div>
  );
}

function RecipeTab({
  active,
  onClick,
  num,
  label,
}: {
  active: boolean;
  onClick: () => void;
  num: string;
  label: string;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex-1 py-2.5 px-3 flex items-center justify-center gap-1.5 border-b-2 transition cursor-pointer ${
        active
          ? 'border-signal bg-surface text-ink-1 font-semibold'
          : 'border-transparent text-ink-2 hover:text-ink-1 hover:bg-well/40'
      }`}
    >
      <span className="font-mono-tech text-[10px] text-ink-3">{num}</span>
      <span>{label}</span>
    </button>
  );
}

function BlockLine({ step, text }: { step: string; text: string }) {
  return (
    <div className="px-4 py-2.5 flex items-center gap-3">
      <span className="text-[10px] text-ink-3">{step}</span>
      <span className="text-ink-1">{text}</span>
    </div>
  );
}
