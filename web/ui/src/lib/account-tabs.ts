import { Bell, BookText, KeyRound, Plug, Folder, History, Monitor, Server, ShieldCheck, type LucideIcon } from 'lucide-react';

// Shared by the settings page, user menu and route validation.
export type AccountTab = 'mcp' | 'control' | 'appearance' | 'notifications' | 'backends' | 'permissions' | 'directories' | 'memory' | 'audit';

export const ACCOUNT_TABS: { tab: AccountTab; label: string; Icon: LucideIcon; detail: string }[] = [
	{ tab: 'control', label: 'MCP access', Icon: KeyRound, detail: 'Dedicated, revocable rights for another Threavia or MCP client to control your resources on this Core.' },
  { tab: 'mcp', label: 'MCP servers', Icon: Plug, detail: 'Existing MCP servers, project activation and backend connections. Credentials stay on the backend.' },
  { tab: 'memory', label: 'Core memory', Icon: BookText, detail: 'Facts and decisions shared across all your projects on this Core. Pinned ones travel with every Job; the others remain searchable.' },
  { tab: 'audit', label: 'Audit', Icon: History, detail: 'Who changed what across your projects and Core memory. Records remain available after a project or its subjects are deleted.' },
  { tab: 'directories', label: 'Directories', Icon: Folder, detail: 'Manage known directories on this Core, in a project or globally across your projects.' },
  { tab: 'permissions', label: 'Permissions', Icon: ShieldCheck, detail: 'The defaults for all your projects and backends on this instance. Backends, projects and sessions can override them.' },
  { tab: 'appearance', label: 'Appearance', Icon: Monitor, detail: 'How this client looks and lays itself out, on this device.' },
  {
    tab: 'notifications',
    label: 'Notifications',
    Icon: Bell,
    detail: 'An approval or a question waiting, or work that ended while you were not watching. Never every event.',
  },
  { tab: 'backends', label: 'Backends', Icon: Server, detail: 'The machines that run the work, and what can be done about them.' },
];
