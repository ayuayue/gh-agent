import { useEffect, useRef } from "react";
import { ActivityIcon } from "../icons";
import type { LogLine } from "../mygo";

export default function Logs({ lines }: { lines: LogLine[] }) {
  const end = useRef<HTMLDivElement>(null);

  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [lines.length]);

  if (lines.length === 0) {
    return (
      <div className="empty">
        <ActivityIcon size={34} />
        <strong>还没有记录</strong>
        <p>点右上角「开始轮询」跑一轮，这里会实时打印命中、出稿和发送的结果。</p>
      </div>
    );
  }

  return (
    <div className="logs">
      {lines.map((l, i) => (
        <div key={i} className={l.level === "error" ? "logrow err" : "logrow"}>
          <time>{l.at}</time>
          <span className="msg">{l.text}</span>
        </div>
      ))}
      <div ref={end} />
    </div>
  );
}
