import { useState } from "react";
import type { Result } from "../mygo";

function lineClass(line: string) {
  if (line.startsWith("+++") || line.startsWith("---")) return "diff-line meta";
  if (line.startsWith("@@")) return "diff-line hunk";
  if (line.startsWith("+")) return "diff-line add";
  if (line.startsWith("-")) return "diff-line del";
  return "diff-line";
}

/** 展示这一轮 agent 对工作区的改动：文件清单 + 可选 diff。 */
export default function Changes({ changes }: { changes: Result }) {
  const [open, setOpen] = useState(false);
  const lines = changes.diff ? changes.diff.split("\n") : [];

  return (
    <section className="changes">
      <header className="changes-head">
        <span className="changes-title">
          代码改动 <em>{changes.files.length}</em>
        </span>
        {lines.length > 0 && (
          <button className="ghost" onClick={() => setOpen((v) => !v)}>
            {open ? "收起 diff" : "看 diff"}
          </button>
        )}
      </header>

      {changes.dirtyBefore && (
        <p className="changes-warn">
          运行前工作区里就有未提交的改动，这份 diff 可能混着你自己改的内容。
        </p>
      )}

      <ul className="file-list">
        {changes.files.map((file) => {
          const isNew = changes.untracked.includes(file);
          return (
            <li key={file}>
              <span className={isNew ? "file-flag new" : "file-flag"}>{isNew ? "新增" : "改动"}</span>
              <code>{file}</code>
            </li>
          );
        })}
      </ul>

      {open && (
        <pre className="diff">
          {lines.map((line, i) => (
            <div key={i} className={lineClass(line)}>
              {line || " "}
            </div>
          ))}
        </pre>
      )}
    </section>
  );
}
