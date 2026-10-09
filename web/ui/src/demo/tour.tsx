import { useNavigate } from '@tanstack/react-router';
import { ArrowLeft, ArrowRight, X } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

import { IDS } from './data';

/**
 * The tour of the demo: the real client, one feature at a time.
 *
 * Each step goes where the feature lives, lights it, and says in two sentences
 * what it is for. The rest of the page stays live under the dimming — a person
 * can stop and try what they are being shown — and the tour picks up where it
 * was. On a phone, where the sidebar is a closed drawer, a step whose element
 * cannot be seen is told in the middle of the screen instead.
 */

interface Step {
  /** Where the feature lives, inside the demo. */
  path: string;
  /** What to light; none for a step said in the middle of the screen. */
  target?: string;
  title: string;
  body: string;
}

const SESSION = (id: string) => `/sessions/${id}`;
const PROJECT = (page: string) => `/projects/${IDS.homelab}/${page}`;

const STEPS: Step[] = [
  {
    path: SESSION(IDS.certManager),
    title: 'Welcome to Threavia',
    body: 'A control plane for coding agents that run on your own machines. Everything here is made up and stays in your browser: click anything, nothing runs.',
  },
  {
    path: SESSION(IDS.certManager),
    target: '[data-tour="sessions"]',
    title: 'Sessions, by project',
    body: 'Each conversation with an agent is a session. The mark on the right says what it is doing — working, waiting for you, or idle — and what waits for you is pinned on top.',
  },
  {
    path: SESSION(IDS.certManager),
    target: '[title="Switch project"]',
    title: 'Projects',
    body: 'A project holds its sessions, tasks, memory and rules. Switching lands you back where you left that project.',
  },
  {
    path: SESSION(IDS.certManager),
    target: '[data-tour="waiting"]',
    title: 'Everything waiting for you',
    body: 'Every approval and every question, across projects, in one place — and on your phone, with a notification, when you are away from this screen.',
  },
  {
    path: SESSION(IDS.certManager),
    target: '[data-tour="attention"]',
    title: 'Decide with the whole picture',
    body: 'The agent stopped before a command its permissions do not allow. You see the command, the machine, the directory and a risk mark, and decide here or from your phone. Try it.',
  },
  {
    path: SESSION(IDS.certManager),
    target: '[data-tour="composer"]',
    title: 'Talk to work in progress',
    body: 'While a job runs, a message can wait its turn, join at the next step, or interrupt. Escape stops the job, as in a terminal.',
  },
  {
    path: SESSION(IDS.ingress),
    target: '[data-tour="steps"]',
    title: 'Finished work folds away',
    body: 'Once a job is done, its thirty commands become one line between your question and the answer. Open it to read every step: what ran, and what came back.',
  },
  {
    path: SESSION(IDS.ingress),
    target: '[data-tour="workspace"]',
    title: 'What changed, file by file',
    body: 'A job ends with the files it touched. Open one to read its diff, asked of the machine that made it.',
  },
  {
    path: SESSION(IDS.grafana),
    target: '[data-tour="artifact"]',
    title: 'What the agent made, shown',
    body: 'A page, a chart, a screenshot: the agent publishes it and it appears here, live — this preview runs its own script, safely apart. It stays with the project’s files.',
  },
  {
    path: `${SESSION(IDS.ingress)}/settings/permissions`,
    target: '[aria-label="Settings"]',
    title: 'A session’s settings',
    body: 'Its own page: permissions that follow the project unless you override them, schedules, the machine it runs on, and archiving.',
  },
  {
    path: `${SESSION(IDS.backups)}/settings/schedules`,
    target: 'main .max-w-3xl > :last-child',
    title: 'Work on a schedule',
    body: 'A session can be sent the same message every morning. A run that would land on busy work is skipped, and the conversation says so.',
  },
  {
    path: PROJECT('tasks'),
    target: 'main section',
    title: 'Tasks the agents keep',
    body: 'Agents file and close tasks as they work. What can start now is kept apart from what waits on other work, and every dependency is named.',
  },
  {
    path: PROJECT('memory'),
    target: 'main ul',
    title: 'Memory that travels',
    body: 'Decisions an agent must not forget. A pinned one goes with every job of the project; click the mark to pin or unpin.',
  },
  {
    path: PROJECT('artifacts'),
    target: 'main ul',
    title: 'Files the work produced',
    body: 'Reports, exports, dashboards: kept with the project, linked to the session that made them. Drop a file on the list to add one.',
  },
  {
    path: PROJECT('skills'),
    target: 'main ul',
    title: 'Skills',
    body: 'Instructions and scripts an agent may use here, installed from git or an archive. View one to read exactly what the agent is given.',
  },
  {
    path: PROJECT('instructions'),
    target: 'main article',
    title: 'Standing rules',
    body: 'What every job of the project reads before it starts, written once and read as rendered.',
  },
  {
    path: PROJECT('permissions'),
    target: '[role="radiogroup"][aria-label="Mode"]',
    title: 'What an agent may do',
    body: 'From asking before every action to acting until a limit. Rules refine the mode: refuse `kubectl delete namespace *`, ask before `helm upgrade`.',
  },
  {
    path: PROJECT('audit'),
    target: 'main ol',
    title: 'Who decided what',
    body: 'Every approval, refusal and change of permissions, said as a sentence, with who did it and from which device.',
  },
  {
    path: '/settings/backends',
    target: 'main ul',
    title: 'Your machines',
    body: 'Work runs on backends — a laptop, a NAS, a CI runner — never on Threavia’s server. One that needs you says what to do.',
  },
  {
    path: SESSION(IDS.backups),
    title: 'Now it is yours',
    body: 'Everything stays clickable: answer the backup question, approve the cert-manager command, file a task, pin a decision. The Demo badge at the top of the sidebar brings the tour back.',
  },
];

const KEY = 'threavia.demo.tour';

function stored(): number | null {
  try {
    const value = window.localStorage.getItem(KEY);
    return value === 'closed' ? null : Number(value ?? 0) || 0;
  } catch {
    return 0;
  }
}

function remember(step: number | null) {
  try {
    window.localStorage.setItem(KEY, step === null ? 'closed' : String(step));
  } catch {
    // The tour starts over next time instead.
  }
}

/** The element a step lights, once it is on screen and not in a closed drawer. */
function visible(selector: string): HTMLElement | null {
  for (const element of document.querySelectorAll<HTMLElement>(selector)) {
    const rect = element.getBoundingClientRect();
    if (rect.width > 0 && rect.height > 0 && rect.right > 0 && rect.left < window.innerWidth) return element;
  }
  return null;
}

const GAP = 12;
const PAD = 6;

export default function DemoTour() {
  const navigate = useNavigate();
  const [step, setStep] = useState<number | null>(stored);
  const [rect, setRect] = useState<DOMRect | null>(null);
  const [searching, setSearching] = useState(false);
  const card = useRef<HTMLDivElement>(null);
  const [cardSize, setCardSize] = useState({ width: 352, height: 200 });

  const current = step === null ? null : STEPS[Math.min(step, STEPS.length - 1)];

  const go = useCallback((next: number | null) => {
    remember(next);
    setStep(next);
  }, []);

  // The badge in the sidebar starts the tour again.
  useEffect(() => {
    const restart = () => go(0);
    window.addEventListener('threavia:tour', restart);
    return () => window.removeEventListener('threavia:tour', restart);
  }, [go]);

  // Going to a step: its page first, then its element once it has rendered.
  useEffect(() => {
    if (!current) return;
    let cancelled = false;
    setRect(null);
    const here = window.location.pathname.replace(/^\/demo/, '') || '/';
    if (here !== current.path) void navigate({ to: current.path as '/' });
    if (!current.target) return;
    setSearching(true);
    const started = Date.now();
    const look = () => {
      if (cancelled) return;
      const element = visible(current.target!);
      if (element) {
        element.scrollIntoView({ block: 'center', behavior: 'smooth' });
        setSearching(false);
        return;
      }
      if (Date.now() - started > 4000) {
        setSearching(false);
        return;
      }
      setTimeout(look, 120);
    };
    look();
    return () => {
      cancelled = true;
    };
  }, [current, navigate]);

  // The light follows its element as the page scrolls, resizes or reflows.
  useEffect(() => {
    if (!current?.target || searching) return;
    let frame = 0;
    const follow = () => {
      const element = visible(current.target!);
      const next = element?.getBoundingClientRect() ?? null;
      setRect((previous) =>
        previous && next && previous.top === next.top && previous.left === next.left && previous.width === next.width && previous.height === next.height
          ? previous
          : next,
      );
      frame = requestAnimationFrame(follow);
    };
    follow();
    return () => cancelAnimationFrame(frame);
  }, [current, searching]);

  useLayoutEffect(() => {
    if (!card.current) return;
    const { width, height } = card.current.getBoundingClientRect();
    if (width !== cardSize.width || height !== cardSize.height) setCardSize({ width, height });
  });

  // ← and → walk the tour, Escape closes it — before anything on the page
  // that would read the same keys, and never while a field is being typed in.
  useEffect(() => {
    if (step === null) return;
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement;
      const elsewhere = target.closest('[role="dialog"]') && !target.closest('[data-tour-card]');
      if (elsewhere || target.closest('input, textarea, select, [contenteditable="true"], [role="menu"]')) return;
      if (event.key === 'Escape') go(null);
      else if (event.key === 'ArrowRight') go(Math.min(step + 1, STEPS.length - 1));
      else if (event.key === 'ArrowLeft') go(Math.max(step - 1, 0));
      else return;
      event.preventDefault();
      event.stopImmediatePropagation();
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [step, go]);

  if (!current || step === null) return null;

  const last = step === STEPS.length - 1;
  const lit = current.target && rect ? rect : null;

  // The card goes beside what is lit when there is room, else under or over
  // it, and in the middle of the screen when nothing is lit.
  const viewport = { width: window.innerWidth, height: window.innerHeight };
  let position: { top: number; left: number };
  if (!lit) {
    position = { top: (viewport.height - cardSize.height) / 2, left: (viewport.width - cardSize.width) / 2 };
  } else if (lit.right + GAP + cardSize.width < viewport.width - GAP) {
    position = { top: lit.top + lit.height / 2 - cardSize.height / 2, left: lit.right + GAP + PAD };
  } else if (lit.bottom + GAP + cardSize.height < viewport.height - GAP) {
    position = { top: lit.bottom + GAP + PAD, left: lit.left + lit.width / 2 - cardSize.width / 2 };
  } else if (lit.top - GAP - cardSize.height > GAP) {
    position = { top: lit.top - GAP - PAD - cardSize.height, left: lit.left + lit.width / 2 - cardSize.width / 2 };
  } else {
    position = { top: viewport.height - cardSize.height - GAP, left: (viewport.width - cardSize.width) / 2 };
  }
  position.top = Math.min(Math.max(position.top, GAP), viewport.height - cardSize.height - GAP);
  position.left = Math.min(Math.max(position.left, GAP), viewport.width - cardSize.width - GAP);

  return (
    <div className="pointer-events-none fixed inset-0 z-[60]" aria-live="polite">
      {/* The dimming is a shadow around the light, so the page under it stays
          live: the tour shows, it does not stop anyone from trying. */}
      {lit ? (
        <div
          className="ring-accent/70 absolute rounded-xl ring-2 transition-[top,left,width,height] duration-300 ease-out motion-reduce:transition-none"
          style={{
            top: lit.top - PAD,
            left: lit.left - PAD,
            width: lit.width + PAD * 2,
            height: lit.height + PAD * 2,
            boxShadow: '0 0 0 9999px oklch(15% 0.01 265 / 0.42)',
          }}
        />
      ) : (
        <div className="absolute inset-0 bg-[oklch(15%_0.01_265/0.42)]" />
      )}

      <div
        ref={card}
        data-tour-card
        role="dialog"
        aria-label={`Tour, step ${step + 1} of ${STEPS.length}: ${current.title}`}
        className="bg-surface border-border pointer-events-auto absolute w-[min(22rem,calc(100vw-1.5rem))] rounded-xl border p-4 shadow-xl transition-[top,left] duration-300 ease-out motion-reduce:transition-none"
        style={position}
      >
        <div className="flex items-center gap-2">
          <span className="text-muted figures font-mono text-xs">
            {step + 1} / {STEPS.length}
          </span>
          {/* How far along, as a line rather than a number to work out. */}
          <span className="bg-surface-2 h-1 flex-1 overflow-hidden rounded-full">
            <span
              className="bg-accent block h-full rounded-full transition-[width] duration-300 motion-reduce:transition-none"
              style={{ width: `${((step + 1) / STEPS.length) * 100}%` }}
            />
          </span>
          <Button
            variant="ghost"
            size="icon"
            className="-mr-1.5 size-11 sm:size-7"
            aria-label="Close the tour"
            title="Close the tour (Esc)"
            onClick={() => go(null)}
          >
            <X />
          </Button>
        </div>
        <h2 className="mt-2 text-[0.9375rem] font-semibold">{current.title}</h2>
        <p className="text-muted mt-1 text-sm leading-relaxed">{current.body}</p>
        <div className="mt-4 flex items-center gap-2">
          {step > 0 && (
            <Button variant="ghost" size="lg" className="gap-1.5" onClick={() => go(step - 1)}>
              <ArrowLeft />
              Back
            </Button>
          )}
          <span className="ml-auto" />
          <Button
            variant="primary"
            size="lg"
            className={cn('gap-1.5', searching && 'opacity-80')}
            onClick={() => go(last ? null : step + 1)}
            autoFocus
          >
            {last ? 'Explore' : step === 0 ? 'Show me' : 'Next'}
            {!last && <ArrowRight />}
          </Button>
        </div>
      </div>
    </div>
  );
}
