import type { Delivery, Event, JobStatus, UserInputRequest, ValidationRequest } from '../api/types';
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
  /** The branch, short (`main ↑1 ●`), and in words for a tooltip. */
  repository?: { text: string; tooltip: string };
  /** A liveness signal, shown only while a Job runs. */
  activity?: string;
  /** Every Job still going, so a message can carry its own stop. */
  pending: Record<string, JobStatus>;
  /** The Job Stop and Escape stop. */
  active?: { id: string; status: JobStatus };
  deliveries: ('NEXT' | 'NOW')[];
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
  | { type: 'sent' }
  | { type: 'sendFailed'; text: string; error: string }
  | { type: 'failed'; message: string };

export type ArtifactRef = { artifactId: string; filename: string; mimeType: string; title?: string };

export type WebviewMessage =
  | { type: 'ready' }
  | { type: 'send'; text: string; delivery: Delivery }
  | { type: 'stop'; jobId: string }
  | { type: 'decide'; id: string; approved: boolean }
  | { type: 'answer'; id: string; value: string }
  | { type: 'loadEarlier' }
  | { type: 'resolvePaths'; paths: string[] }
  | { type: 'openPath'; path: string }
  | { type: 'openLink'; href: string }
  | { type: 'openDiff'; sequence: number; path: string }
  | ({ type: 'openArtifact' } & ArtifactRef)
  | ({ type: 'saveArtifact' } & ArtifactRef);

/** What the webview keeps across a reload, through the editor's webview state. */
export interface PersistedState {
  /** Empty while the panel is a draft. */
  sessionId: string;
  /** What a draft starts its Session with, until the first message is sent. */
  start?: DraftStart;
  draft?: string;
  delivery?: Delivery;
  /** The folds and tool calls the reader opened. */
  opened?: number[];
  expanded?: string[];
}
