import { Server, Sparkles, SquareTerminal } from 'lucide-react';

export function backendIcon(provider?: string) {
  switch (provider?.toLowerCase()) {
    case 'claude': return Sparkles;
    case 'codex': return SquareTerminal;
    default: return Server;
  }
}

export function quotaNumber(value: number) {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(value);
}
