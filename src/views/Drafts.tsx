import { useState } from "react";
import { CheckIcon, ChevronIcon, ExternalIcon, InboxIcon, XIcon } from "../icons";
import Markdown from "../Markdown";
import { Service, type Draft } from "../mygo";

const STATUS_LABEL: Record<string, string> = {
  pending: "待审",
  posted: "已发送",
  rejected: "已驳回",
  failed: "发送失败",
};

const CLAMP_AT = 320;

function fmtTime(iso: string) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, "0");
  return p(d.getMonth() + 1) + "-" + p(d.getDate()) + " " + p(d.getHours()) + ":" + p(d.getMinutes());
}

export default function Drafts({ drafts, onChanged }: { drafts: Draft[]; onChanged: () => void }) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState<string[]>([]);

  const pending = drafts.filter((d) => d.status === "pending");
  const history = drafts.filter((d) => d.status !== "pending");

  const toggle = (id: string) =>
    setExpanded((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));

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

  if (pending.length === 0 && history.length === 0) {
    return (
      <div className="empty">
        <InboxIcon size={34} />
        <strong>还没有待审的回复</strong>
        <p>
          轮询命中触发词后，草稿会出现在这里。触发词现在是配置里的
          <code>trigger</code>，默认 <code>@claude</code>。
        </p>
      </div>
    );
  }

  return (
    <div className="drafts">
      {error && <div className="banner error">{error}</div>}

      {pending.length === 0 && (
        <div className="empty">
          <InboxIcon size={34} />
          <strong>队列已清空</strong>
          <p>新命中会实时出现在这里，不用手动刷新。</p>
        </div>
      )}

      {pending.map((d) => {
        const open = expanded.includes(d.id);
        const long = d.body.length > CLAMP_AT;
        return (
          <article key={d.id} className="draft">
            <div className="draft-head">
              <button
                className="draft-ref"
                onClick={() => void Service.openExternal(d.issueUrl)}
                title="在浏览器打开"
              >
                {d.repo}#{d.number}
              </button>
              <span className="draft-meta">
                <span className="author">{d.author}</span>
                <span className="sep">·</span>
                <time>{fmtTime(d.createdAt)}</time>
              </span>
            </div>

            <h3 className="draft-title">{d.title}</h3>

            <div className={open || !long ? "draft-body" : "draft-body clamped"}>
              <Markdown text={d.body} />
            </div>

            {long && (
              <button className="ghost draft-toggle" onClick={() => toggle(d.id)}>
                <ChevronIcon size={13} />
                {open ? "收起" : "展开全文"}
              </button>
            )}

            <div className="draft-foot">
              <button className="ghost" onClick={() => void Service.openExternal(d.commentUrl)}>
                <ExternalIcon size={14} />
                原评论
              </button>
              <span className="spacer" />
              <button className="danger" onClick={() => act(d.id, false)} disabled={busy === d.id}>
                <XIcon size={14} />
                驳回
              </button>
              <button className="primary" onClick={() => act(d.id, true)} disabled={busy === d.id}>
                <CheckIcon size={14} />
                {busy === d.id ? "发送中…" : "批准并发送"}
              </button>
            </div>
          </article>
        );
      })}

      {history.length > 0 && (
        <>
          <div className="section-label">历史 · {history.length}</div>
          <div className="history">
            {history.slice(0, 50).map((d) => (
              <div key={d.id} className="hrow">
                <span className={"tag " + d.status}>{STATUS_LABEL[d.status] ?? d.status}</span>
                <button className="link ref" onClick={() => void Service.openExternal(d.issueUrl)}>
                  {d.repo}#{d.number}
                </button>
                <span className="grow" title={d.error || d.title}>
                  {d.error || d.title}
                </span>
                <time>{fmtTime(d.createdAt)}</time>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
