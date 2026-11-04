import type { BackendInstance, BackendQuotas, Delivery, Event, Job, JobStatus, Repository, UserInputRequest, ValidationRequest } from '../api/types';
import type { DraftStart } from './draft';
import type { StateTone } from './state';

/**
 * What the extension and a conversation webview say to each other.
 *
 * The extension holds the Session — snapshot, stream, requests, credentials —
 * and the webview only draws it and reports what the person did: the page has
 * no network access of its own, by its Content-Security-Policy, so nothing it
 * renders can reach Core or anything else.
 */

/** Everything about the Session that is not the timeline itself. */
export interface SessionView {
  title: string;
  status: { label: string; tone: StateTone };
  /** Which machine does the work: first-class, never a detail. */
  backend?: string;
  backendType?: string;
  backendTooltip?: string;
  backendQuotas?: BackendQuotas;
  modelSettings: { model: string; effort: string; supported: boolean; catalog?: BackendInstance['modelCatalog']; saving?: boolean; error?: string };
  learnings?: { sourceJobId?: string; pending: boolean; error?: string };
  /** The compact branch label and the Git status shown in its hover panel. */
  repository?: { text: string; status: Repository; fetching?: boolean; error?: string };
  /** A liveness signal, shown only while a Job runs. */
  activity?: string;
  /** Every Job still going, so a message can carry its own stop. */
  pending: Record<string, JobStatus>;
  /** The Job Stop and Escape stop. */
  active?: Pick<Job, 'id' | 'status' | 'model' | 'effort' | 'backendWait'>;
  deliveries: ('NEXT' | 'NOW')[];
  /** What a Job waiting for its backend could take later, shown unavailable. */
  held?: ('NEXT' | 'NOW')[];
  validations: ValidationRequest[];
  questions: UserInputRequest[];
  /** Whether history goes further back than what was sent. */
  more: boolean;
}

export type HostMessage =
  | { type: 'session'; view: SessionView }
  /**
   * A new Session not created yet: what was chosen for it, so the page says
   * where the first message goes and keeps it across a reload.
   */
  | { type: 'draft'; start: DraftStart }
  /** The first message created the Session: the page is that Session's now. */
  | { type: 'started'; sessionId: string }
  /** The timeline: replaced when `reset`, otherwise merged by sequence. */
  | { type: 'events'; events: Event[]; reset?: boolean }
  | { type: 'loadingEarlier'; loading: boolean }
  | { type: 'paths'; resolved: Record<string, boolean> }
  /** An answer given from this panel: `ok` once it landed, so the card can say so. */
  | { type: 'answered'; id: string; ok: boolean }
  | { type: 'copiedResponse'; sequence: number; ok: boolean; error?: string }
  | { type: 'media'; artifactId: string; source?: string; error?: string }
  | { type: 'attached'; artifact: ArtifactRef & { size: number } }
  | { type: 'attachFailed'; error: string }
  | { type: 'sent' }
  | { type: 'sendFailed'; text: string; error: string }
  | { type: 'archiveFinished' }
  | { type: 'failed'; message: string }
  /**
   * What the field offers after / and @: the provider that reads the Skills,
   * the Skills a Job here would have, and whether its backend lists files.
   */
  | { type: 'composer'; provider?: string; skills: { name: string; description?: string }[]; files: boolean }
  | { type: 'files'; query: string; paths: string[]; truncated: boolean; error?: string }
  /** A queued message taken back: it goes back in the field, files included. */
  | { type: 'withdrawn'; text: string; attachments: (ArtifactRef & { size: number })[] };

export type ArtifactRef = { artifactId: string; filename: string; mimeType: string; title?: string };

export type WebviewMessage =
  | { type: 'ready' }
  /** Whether the keyboard focus is in the page, which is when the person is reading it. */
  | { type: 'focus'; focused: boolean }
  | { type: 'send'; text: string; delivery: Delivery; artifactIds?: string[] }
  | { type: 'attach'; filename: string; mimeType: string; content: string }
  | { type: 'loadMedia'; artifactId: string }
  | { type: 'stop'; jobId: string }
  | { type: 'withdraw'; jobId: string }
  | { type: 'files'; query: string }
  | { type: 'decide'; id: string; approved: boolean }
  | { type: 'answer'; id: string; value: string }
  | { type: 'loadEarlier' }
  | { type: 'copyResponse'; sequence: number }
  | { type: 'refreshRepository' }
  | { type: 'setModelSettings'; model: string; effort: string }
  /** `/rename <name>`: `text` is the command, given back if renaming fails. */
  | { type: 'rename'; title: string; text: string }
  /** The model pill: the extension shows the choice in the editor's Quick Pick. */
  | { type: 'pickModel' }
  | { type: 'archive' }
  | { type: 'captureLearnings' }
  | { type: 'resolvePaths'; paths: string[] }
  | { type: 'openPath'; path: string }
  | { type: 'openLink'; href: string }
  | { type: 'openDiff'; sequence: number; path: string }
  | ({ type: 'openArtifact' } & ArtifactRef)
  | ({ type: 'saveArtifact' } & ArtifactRef);

/** What the webview keeps across a reload, through the editor's webview state. */
export interface PersistedState {
  /** The Core the Session or draft belongs to; missing in a state from before there were several. */
  coreId?: string;
  /** Empty while the panel is a draft. */
  sessionId: string;
  /** What a draft starts its Session with, until the first message is sent. */
  start?: DraftStart;
  draft?: string;
  attachments?: (ArtifactRef & { size: number })[];
  delivery?: Delivery;
  fullWidth?: boolean;
  /** The folds and tool calls the reader opened. */
  opened?: number[];
  expanded?: string[];
}
