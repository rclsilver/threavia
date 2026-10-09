/**
 * What a tool call would do, said as a person deciding it needs it said: the
 * web client's reading (web/ui/src/lib/risk.ts), so an approval is announced
 * the same way in both.
 *
 * Read from the tool and its input, never trusted as a guarantee: the policy
 * enforced by Threavia and the backend is what bounds the agent. This only
 * decides how loudly an approval asks to be read.
 */

export type Risk = { label: string; tone: 'neutral' | 'warn' | 'danger' };

const DESTRUCTIVE =
  /\b(rm\s+-[a-z]*[rf]|rmdir|mkfs|dd\s+if=|shred|kubectl\s+(delete|drain|cordon)|helm\s+(uninstall|delete|rollback)|(tofu|terraform)\s+(apply|destroy|import|state\s+rm)|drop\s+(table|database)|truncate\s+table|git\s+(reset\s+--hard|clean\s+-[a-z]*f|branch\s+-D))\b/i;
const PUSH = /\bgit\s+(-C\s+\S+\s+)?push\b/i;
const NETWORK = /\b(curl|wget|ssh|scp|rsync|nc|ftp|telnet)\b/i;

export function riskOf(tool: string, input: Record<string, unknown>): Risk {
  const command = typeof input.command === 'string' ? input.command : '';
  switch (tool) {
    case 'Bash':
      if (PUSH.test(command)) return { label: 'Pushes to a remote', tone: 'danger' };
      if (DESTRUCTIVE.test(command)) return { label: 'Deletes or changes infrastructure', tone: 'danger' };
      if (NETWORK.test(command)) return { label: 'Runs a command on the network', tone: 'warn' };
      return { label: 'Runs a command', tone: 'warn' };
    case 'Write':
      return { label: 'Writes a file', tone: 'warn' };
    case 'Edit':
    case 'MultiEdit':
    case 'NotebookEdit':
      return { label: 'Changes a file', tone: 'warn' };
    case 'WebFetch':
    case 'WebSearch':
      return { label: 'Reaches the network', tone: 'warn' };
    case 'Read':
    case 'Glob':
    case 'Grep':
    case 'LS':
      return { label: 'Reads files', tone: 'neutral' };
    default:
      return { label: 'Uses a tool', tone: 'neutral' };
  }
}
