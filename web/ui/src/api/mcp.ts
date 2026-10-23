import type { components } from './schema';
export type MCPServer = components['schemas']['MCPServer'];
export type MCPBinding = components['schemas']['MCPBinding'];
export type MCPRestriction = components['schemas']['MCPRestriction'];
export interface MCPProbe { state: string; serverId: string; tools: { name: string; description?: string }[]; resources: boolean; prompts: boolean }
export interface MCPImport { servers: { declaration: MCPServer; notes: string[] }[]; preview: boolean }
