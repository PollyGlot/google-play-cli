import { useRef, useState } from 'react';

type Tab = {
  id: string;
  label: string;
  command: string;
  note?: string;
};

const TABS: Tab[] = [
  {
    id: 'brew',
    label: 'Homebrew',
    command: 'brew install PollyGlot/tap/gplay',
    note: 'macOS and Linux.',
  },
  {
    id: 'script',
    label: 'Install script',
    command:
      'curl -fsSL https://raw.githubusercontent.com/PollyGlot/google-play-cli/main/install.sh | sh',
    note: 'Detects your OS and architecture; ideal for CI images.',
  },
  {
    id: 'go',
    label: 'go install',
    command: 'go install github.com/PollyGlot/google-play-cli/cmd/gplay@latest',
    note: 'Builds from source with your Go toolchain.',
  },
  {
    id: 'binaries',
    label: 'Binaries',
    command: 'https://github.com/PollyGlot/google-play-cli/releases',
    note: 'Pre-built archives for Linux, macOS, and Windows — with checksums and signatures.',
  },
];

export default function InstallTabs() {
  const [active, setActive] = useState(TABS[0]);
  const [copied, setCopied] = useState(false);
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([]);

  const select = (tab: Tab) => {
    setActive(tab);
    setCopied(false);
  };

  // Roving focus per the WAI-ARIA tabs pattern: arrows/Home/End move focus
  // and selection together.
  const onKeyDown = (event: React.KeyboardEvent, index: number) => {
    let next: number;
    switch (event.key) {
      case 'ArrowRight':
        next = (index + 1) % TABS.length;
        break;
      case 'ArrowLeft':
        next = (index - 1 + TABS.length) % TABS.length;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = TABS.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    select(TABS[next]);
    tabRefs.current[next]?.focus();
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(active.command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard unavailable (permissions / non-secure context) — no-op.
    }
  };

  return (
    <div className="w-full">
      <div role="tablist" aria-label="Installation methods" className="flex flex-wrap gap-1">
        {TABS.map((tab, index) => (
          <button
            key={tab.id}
            ref={(el) => {
              tabRefs.current[index] = el;
            }}
            role="tab"
            id={`install-tab-${tab.id}`}
            aria-selected={active.id === tab.id}
            aria-controls={`install-panel-${tab.id}`}
            tabIndex={active.id === tab.id ? 0 : -1}
            onClick={() => select(tab)}
            onKeyDown={(event) => onKeyDown(event, index)}
            className={`rounded-t-md px-4 py-2 font-mono text-sm transition-colors ${
              active.id === tab.id
                ? 'bg-zinc-900 text-brand'
                : 'bg-transparent text-zinc-400 hover:text-zinc-200'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div
        role="tabpanel"
        id={`install-panel-${active.id}`}
        aria-labelledby={`install-tab-${active.id}`}
        className="rounded-b-md rounded-tr-md border border-zinc-800 bg-zinc-900 p-4"
      >
        <div className="flex items-start justify-between gap-3">
          <code className="block overflow-x-auto py-1 font-mono text-sm leading-relaxed text-zinc-100">
            {active.id === 'binaries' ? (
              <a href={active.command} className="text-brand underline underline-offset-4">
                {active.command}
              </a>
            ) : (
              <>
                <span className="select-none text-brand">$ </span>
                {active.command}
              </>
            )}
          </code>
          {active.id !== 'binaries' && (
            <button
              onClick={copy}
              aria-label="Copy command"
              className="shrink-0 rounded-md border border-zinc-700 px-3 py-1.5 text-xs text-zinc-300 transition-colors hover:border-brand hover:text-brand"
            >
              {copied ? 'Copied ✓' : 'Copy'}
            </button>
          )}
        </div>
        {active.note && <p className="mt-3 text-sm text-zinc-500">{active.note}</p>}
      </div>
    </div>
  );
}
