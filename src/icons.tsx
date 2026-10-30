// 统一图标集（Lucide 风格，24x24 viewBox，stroke 跟随 currentColor）。
// 不用 emoji 当图标。

export type IconProps = { size?: number; className?: string };

function Svg({ size = 16, className, children }: IconProps & { children: React.ReactNode }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      {children}
    </svg>
  );
}

export function InboxIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M22 12h-6l-2 3h-4l-2-3H2" />
      <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" />
    </Svg>
  );
}

export function ActivityIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M22 12h-4l-3 9L9 3l-3 9H2" />
    </Svg>
  );
}

export function SlidersIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3" />
      <path d="M1 14h6M9 8h6M17 16h6" />
    </Svg>
  );
}

export function PlayIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M6 4.5v15l13-7.5z" fill="currentColor" stroke="none" />
    </Svg>
  );
}

export function PauseIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <rect x="6" y="5" width="4" height="14" rx="1" fill="currentColor" stroke="none" />
      <rect x="14" y="5" width="4" height="14" rx="1" fill="currentColor" stroke="none" />
    </Svg>
  );
}

export function RefreshIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M20.5 12a8.5 8.5 0 1 1-2.6-6.1" />
      <path d="M20.5 4v5h-5" />
    </Svg>
  );
}

export function ExternalIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
      <path d="M15 3h6v6" />
      <path d="M10 14 21 3" />
    </Svg>
  );
}

export function CheckIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M20 6 9 17l-5-5" />
    </Svg>
  );
}

export function XIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M18 6 6 18M6 6l12 12" />
    </Svg>
  );
}

export function ChevronIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="m6 9 6 6 6-6" />
    </Svg>
  );
}

export function AlertIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7.5v5.5M12 16.5h.01" />
    </Svg>
  );
}

export function ListIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M8 6h13M8 12h13M8 18h13" />
      <path d="M3 6h.01M3 12h.01M3 18h.01" />
    </Svg>
  );
}

export function EyeOffIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M10.7 5.1A10.9 10.9 0 0 1 12 5c6 0 10 7 10 7a17.6 17.6 0 0 1-2.2 3.1M6.6 6.6C3.6 8.5 2 12 2 12s4 7 10 7a10.7 10.7 0 0 0 5.4-1.5" />
      <path d="M9.9 9.9a3 3 0 0 0 4.2 4.2" />
      <path d="M2 2l20 20" />
    </Svg>
  );
}

export function GitCommitIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <circle cx="12" cy="12" r="3.5" />
      <path d="M3 12h5.5M15.5 12H21" />
    </Svg>
  );
}

export function MessageIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M21 11.5a8.4 8.4 0 0 1-9 8.4 9.4 9.4 0 0 1-3.8-.8L3 21l1.9-5.1A8.4 8.4 0 0 1 12 3.1a8.4 8.4 0 0 1 9 8.4z" />
    </Svg>
  );
}


export function SparklesIcon(p: IconProps) {
  return (
    <Svg {...p}>
      <path d="M12 3.5l1.8 4.7 4.7 1.8-4.7 1.8L12 16.5l-1.8-4.7L5.5 10l4.7-1.8z" />
      <path d="M18.5 15.5l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z" />
    </Svg>
  );
}

