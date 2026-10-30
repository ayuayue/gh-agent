import { useState } from "react";
import Markdown from "../Markdown";
import { Service, type Draft } from "../mygo";

const STATUS_LABEL: Record<string, string> = {
  pending: "待审",
  posted: "已发送",
  rejected: "已驳回",
  failed: "发送失败",
};

export default function Drafts({ drafts, onChanged }: { drafts: Draft[]; onChanged: () => void }) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [showHistory, setShowHistory] = useState(false);

  const pending = drafts.filter((d) => d.status === "pending");
  const history = drafts.filter((d) => d.status !== "pending");

  const act = async (id: string, approve: boolean) => {
    setBusy(id);
    setError("");
    try {
      if (approve) await Service.approve(id);
      else await Service.reject(id);
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="drafts">
      {error && <div className="alert">{error}</div>}

      {pending.length === 0 && (
        <p className="empty">没有待审的回复。命中触发词后，草稿会出现在这里。</p>
      )}

      {pending.map((d) => (
        <article key={d.id} className="card">
          <header>
            <a
              href={d.issueUrl}
              onClick={(e) => {
                e.preventDefault();
                void Service.openExternal(d.issueUrl);
              }}
            >
              {d.repo}#{d.number}
            </a>
            <span className="title">{d.title}</span>
            <span className="muted">来自 {d.author}</span>
            <span className="muted">{d.createdAt.replace("T", " ").slice(0, 16)}</span>
          </header>
          <Markdown text={d.body} />
          <footer>
            <a
              href={d.commentUrl}
              onClick={(e) => {
                e.preventDefault();
                void Service.openExternal(d.commentUrl);
              }}
            >
              看原评论
            </a>
            <span className="spacer" />
            <button onClick={() => act(d.id, false)} disabled={busy === d.id}>
              驳回
            </button>
            <button className="primary" onClick={() => act(d.id, true)} disabled={busy === d.id}>
              {busy === d.id ? "发送中…" : "批准并发送"}
            </button>
          </footer>
        </article>
      ))}

      {history.length > 0 && (
        <section className="history">
          <button className="link" onClick={() => setShowHistory((v) => !v)}>
            {showHistory ? "收起历史" : `历史（${history.length}）`}
          </button>
          {showHistory &&
            history.slice(0, 30).map((d) => (
              <div key={d.id} className="row">
                <span className={`tag ${d.status}`}>{STATUS_LABEL[d.status] ?? d.status}</span>
                <span>
                  {d.repo}#{d.number}
                </span>
                <span className="muted grow">{d.title}</span>
                <span className="muted">{d.createdAt.replace("T", " ").slice(5, 16)}</span>
                {d.error && <span className="err">{d.error}</span>}
              </div>
            ))}
        </section>
      )}
    </div>
  );
}
