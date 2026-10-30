import { useEffect, useRef } from "react";
import type { LogLine } from "../mygo";

export default function Logs({ lines }: { lines: LogLine[] }) {
  const end = useRef<HTMLDivElement>(null);

  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [lines.length]);

  if (lines.length === 0) {
    return <p className="empty">还没有日志。点「开始」跑一轮试试。</p>;
  }

  return (
    <div className="logs">
      {lines.map((l, i) => (
        <div key={i} className="logline">
          <span className="muted">{l.at}</span>
          <span>{l.text}</span>
        </div>
      ))}
      <div ref={end} />
    </div>
  );
}
