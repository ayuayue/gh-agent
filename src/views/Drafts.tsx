import { useEffect, useState } from "react";
import { CheckIcon, ChevronIcon, ExternalIcon, InboxIcon, RefreshIcon, XIcon } from "../icons";
import Markdown from "../Markdown";
import { Service, type AgentInfo, type Draft } from "../mygo";
import Changes from "./Changes";

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
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [editing, setEditing] = useState<string | null>(null);
  const [draftText, setDraftText] = useState("");
  const [redo, setRedo] = useState<Record<string, string>>({});

  useEffect(() => {
    Service.agents()
      .then(setAgents)
      .catch(() => setAgents([]));
  }, []);

  const pending = drafts.filter((d) => d.status === "pending");
  const history = drafts.filter((d) => d.status !== "pending");

  const toggle = (id: string) =>
    setExpanded((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));

  // busy 记 "id|动作"，这样卡片上每个按钮只在自己那件事进行时才变样。
  const act = async (id: string, approve: boolean) => {
    setBusy(id + (approve ? "|approve" : "|reject"));
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

  const startEdit = (d: Draft) => {
    setEditing(d.id);
    setDraftText(d.body);
    setError("");
  };

  const saveEdit = async (id: string) => {
    setBusy(id + "|edit");
    setError("");
    try {
      await Service.updateDraft(id, draftText);
      setEditing(null);
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  const redraft = async (d: Draft) => {
    const agentID = redo[d.id] ?? "";
    if (!agentID) return;
    setBusy(d.id + "|redraft");
    setError("");
    try {
      await Service.redraft(d.id, agentID);
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
          轮询命中触发词后，草稿会出现在这里。触发词是配置里的 <code>trigger</code>，默认
          <code>@claude</code>。
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
        const isEditing = editing === d.id;
        const kind = busy.startsWith(d.id + "|") ? busy.slice(d.id.length + 1) : "";
        const working = kind !== "";
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
                {d.agent && <span className="agent-chip">{d.agent}</span>}
              </span>
            </div>

            <h3 className="draft-title">{d.title}</h3>

            {isEditing ? (
              <div className="draft-body">
                <textarea
                  className="editor"
                  value={draftText}
                  onChange={(e) => setDraftText(e.target.value)}
                  rows={14}
                  spellCheck={false}
                />
              </div>
            ) : (
              <div className={open || !long ? "draft-body" : "draft-body clamped"}>
                <Markdown text={d.body} />
              </div>
            )}

            {!isEditing && long && (
              <button className="ghost draft-toggle" onClick={() => toggle(d.id)}>
                <ChevronIcon size={13} />
                {open ? "收起" : "展开全文"}
              </button>
            )}

            {!isEditing && d.changes && <Changes changes={d.changes} />}

            <div className="draft-foot">
              {isEditing ? (
                <>
                  <span className="spacer" />
                  <button className="ghost" onClick={() => setEditing(null)}>
                    取消
                  </button>
                  <button className="primary" onClick={() => saveEdit(d.id)} disabled={working}>
                    <CheckIcon size={14} />
                    {kind === "edit" ? "保存中…" : "保存修改"}
                  </button>
                </>
              ) : (
                <>
                  <button className="ghost" onClick={() => void Service.openExternal(d.commentUrl)}>
                    <ExternalIcon size={14} />
                    原评论
                  </button>
                  <button className="ghost" onClick={() => startEdit(d)}>
                    编辑
                  </button>
                  <select
                    className="agent-select"
                    value={redo[d.id] ?? ""}
                    onChange={(e) => setRedo((prev) => ({ ...prev, [d.id]: e.target.value }))}
                    title="换一个 agent 重做"
                  >
                    <option value="">换 agent…</option>
                    {agents
                      .filter((a) => a.available)
                      .map((a) => (
                        <option key={a.id} value={a.id}>
                          {a.name}
                        </option>
                      ))}
                  </select>
                  <button
                    className="ghost"
                    onClick={() => redraft(d)}
                    disabled={working || !(redo[d.id] ?? "")}
                  >
                    <RefreshIcon size={14} />
                    {kind === "redraft" ? "重做中…" : "重做"}
                  </button>
                  <span className="spacer" />
                  <button className="danger" onClick={() => act(d.id, false)} disabled={working}>
                    <XIcon size={14} />
                    {kind === "reject" ? "驳回中…" : "驳回"}
                  </button>
                  <button className="primary" onClick={() => act(d.id, true)} disabled={working}>
                    <CheckIcon size={14} />
                    {kind === "approve" ? "发送中…" : "批准并发送"}
                  </button>
                </>
              )}
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
