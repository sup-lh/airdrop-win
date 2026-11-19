import { useEffect, useState } from 'react';

const THEME_KEY = 'airdropwin_theme';
const SOUND_KEY = 'airdropwin_sound';

export type ThemeMode = 'light' | 'dark' | 'system';

export function useTheme() {
  const [mode, setMode] = useState<ThemeMode>(() => {
    try {
      const saved = localStorage.getItem(THEME_KEY) as ThemeMode | null;
      if (saved === 'light' || saved === 'dark' || saved === 'system') return saved;
    } catch {
      // ignore
    }
    return 'system';
  });

  const [isDark, setIsDark] = useState(false);

  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => {
      const dark = mode === 'dark' || (mode === 'system' && mq.matches);
      setIsDark(dark);
      document.documentElement.classList.toggle('dark', dark);
    };
    apply();
    mq.addEventListener('change', apply);
    try {
      localStorage.setItem(THEME_KEY, mode);
    } catch {
      // ignore
    }
    return () => mq.removeEventListener('change', apply);
  }, [mode]);

  const toggle = () => {
    setMode((prev) => {
      const currentlyDark =
        prev === 'dark' || (prev === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);
      return currentlyDark ? 'light' : 'dark';
    });
  };

  return { mode, isDark, setMode, toggle };
}

export function useSoundPref() {
  const [enabled, setEnabled] = useState<boolean>(() => {
    try {
      const saved = localStorage.getItem(SOUND_KEY);
      return saved === null ? true : saved === '1';
    } catch {
      return true;
    }
  });

  const toggle = () => {
    setEnabled((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(SOUND_KEY, next ? '1' : '0');
      } catch {
        // ignore
      }
      if (next) playChime('pop');
      return next;
    });
  };

  return { enabled, toggle };
}

let audioCtx: AudioContext | null = null;

/**
 * Plays a soft Apple-inspired crystalline chime using pure Web Audio API (zero audio assets).
 */
export function playChime(kind: 'receive' | 'send' | 'pop' = 'receive') {
  try {
    const saved = localStorage.getItem(SOUND_KEY);
    if (saved === '0') return;
  } catch {
    // ignore
  }
  try {
    const Ctx = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
    if (!Ctx) return;
    if (!audioCtx) audioCtx = new Ctx();
    if (audioCtx.state === 'suspended') {
      audioCtx.resume().catch(() => {});
    }

    const now = audioCtx.currentTime;
    const notes =
      kind === 'receive'
        ? [
            { freq: 880, start: 0, dur: 0.16, gain: 0.06 },
            { freq: 1318.5, start: 0.085, dur: 0.24, gain: 0.055 },
          ]
        : kind === 'send'
        ? [
            { freq: 659.25, start: 0, dur: 0.12, gain: 0.045 },
            { freq: 987.77, start: 0.06, dur: 0.16, gain: 0.045 },
          ]
        : [{ freq: 784, start: 0, dur: 0.09, gain: 0.04 }];

    for (const n of notes) {
      const osc = audioCtx.createOscillator();
      const g = audioCtx.createGain();
      osc.type = 'sine';
      osc.frequency.setValueAtTime(n.freq, now + n.start);
      g.gain.setValueAtTime(0.0001, now + n.start);
      g.gain.exponentialRampToValueAtTime(n.gain, now + n.start + 0.015);
      g.gain.exponentialRampToValueAtTime(0.0001, now + n.start + n.dur);
      osc.connect(g);
      g.connect(audioCtx.destination);
      osc.start(now + n.start);
      osc.stop(now + n.start + n.dur + 0.02);
    }
  } catch {
    // ignore audio autoplay blocks
  }
}
