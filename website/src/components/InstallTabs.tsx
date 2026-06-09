import { useState } from 'react';

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
        {TABS.map((tab) => (
          <button
            key={tab.id}
            role="tab"
            aria-selected={active.id === tab.id}
            onClick={() => {
              setActive(tab);
              setCopied(false);
            }}
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
      <div role="tabpanel" className="rounded-b-md rounded-tr-md border border-zinc-800 bg-zinc-900 p-4">
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
