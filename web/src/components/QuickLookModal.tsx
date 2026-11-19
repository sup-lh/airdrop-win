import React, { useEffect } from 'react';
import { ArrowDownToLine, ExternalLink, FolderOpen, X } from 'lucide-react';

interface Props {
  src: string;
  title: string;
  subtitle?: string;
  downloadHref?: string;
  onOpenNative?: () => void;
  onRevealNative?: () => void;
  onClose: () => void;
}

export default function QuickLookModal({
  src,
  title,
  subtitle,
  downloadHref,
  onOpenNative,
  onRevealNative,
  onClose,
}: Props) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div
      onClick={onClose}
      className="fixed inset-0 z-50 bg-black/70 backdrop-blur-xs flex items-center justify-center p-4 sm:p-8"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="bg-surface border border-hairline-strong rounded-md max-w-4xl w-full max-h-[90vh] flex flex-col overflow-hidden shadow-2xl animate-sheet"
      >
        <div className="flex items-center justify-between gap-3 px-4 py-3 border-b border-hairline bg-elevated">
          <div className="min-w-0">
            <div className="text-[13px] font-semibold text-ink-1 truncate">{title}</div>
            {subtitle && <div className="text-[11px] text-ink-3 font-mono-tech mt-0.5">{subtitle}</div>}
          </div>
          <div className="flex items-center gap-1.5 shrink-0">
            {onOpenNative && (
              <button
                onClick={onOpenNative}
                className="keycap-btn inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-sm border border-hairline bg-surface hover:bg-well text-xs font-medium text-ink-1 cursor-pointer"
              >
                <ExternalLink className="w-3.5 h-3.5" />
                打开
              </button>
            )}
            {onRevealNative && (
              <button
                onClick={onRevealNative}
                className="keycap-btn inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-sm border border-hairline bg-surface hover:bg-well text-xs font-medium text-ink-1 cursor-pointer"
              >
                <FolderOpen className="w-3.5 h-3.5" />
                定位
              </button>
            )}
            {downloadHref && (
              <a
                href={downloadHref}
                download={title}
                className="keycap-btn inline-flex items-center gap-1.5 px-3 py-1.5 rounded-sm bg-signal text-white text-xs font-medium"
              >
                <ArrowDownToLine className="w-3.5 h-3.5" />
                保存
              </a>
            )}
            <button
              onClick={onClose}
              className="keycap-btn p-1.5 rounded-sm border border-hairline bg-surface hover:bg-well text-ink-2 cursor-pointer"
              aria-label="关闭预览"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>

        <div className="flex-1 overflow-auto p-6 flex items-center justify-center bg-canvas swiss-dot-grid">
          <img src={src} alt={title} className="max-w-full max-h-[72vh] object-contain border border-hairline shadow-xl" />
        </div>
      </div>
    </div>
  );
}
