import { useEffect, useMemo, useState } from "react";
import {
  CheckIcon,
  ChevronIcon,
  ExternalIcon,
  EyeOffIcon,
  InboxIcon,
  RefreshIcon,
  XIcon,
} from "../icons";
import Markdown from "../Markdown";
import { Service, type AgentInfo, type Draft } from "../mygo";
import Changes from "./Changes";

const STATUS_LABEL: Record<string, string> = {
  pending: "待审",
  posted: "已发送",
  rejected: "已驳回",
  failed: "发送失败",
  ignored: "已忽略",
};

const CLAMP_AT = 320;

function fmtTime(iso: string) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, "0");
  return p(d.getMonth() + 1) + "-" + p(d.getDate()) + " " + p(d.getHours()) + ":" + p(d.getMinutes());
}

export default function Drafts({
  drafts,
  processing,
  onChanged,
}: {
  drafts: Draft[];
  processing: boolean;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState<string[]>([]);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [editing, setEditing] = useState<string | null>(null);
  const [draftText, setDraftText] = useState("");
  const [redo, setRedo] = useState<Record<string, string>>({});
  const [ignoring, setIgnoring] = useState<string | null>(null);
  const [ignoreNote, setIgnoreNote] = useState("");
  const [repoFilter, setRepoFilter] = useState("");

  useEffect(() => {
    Service.agents()
      .then(setAgents)
      .catch(() => setAgents([]));
  }, []);

  const repos = useMemo(() => [...new Set(drafts.map((d) => d.repo))].sort(), [drafts]);
  const scoped = repoFilter ? drafts.filter((d) => d.repo === repoFilter) : drafts;
  const pending = scoped.filter((d) => d.status === "pending");
  const history = scoped.filter((d) => d.status !== "pending");

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

  const startIgnore = (d: Draft) => {
    setIgnoring(d.id);
    setIgnoreNote("");
    setError("");
  };

  const doIgnore = async (id: string) => {
    setBusy(id + "|ignore");
    setError("");
    try {
      await Service.ignore(id, ignoreNote);
      setIgnoring(null);
      setIgnoreNote("");
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  const restore = async (id: string) => {
    setBusy(id + "|restore");
    setError("");
    try {
      await Service.unignore(id);
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  // 默认用产出这条草稿的 agent 重做；下拉可以换成别的。
  const agentIdOf = (d: Draft) => agents.find((a) => a.name === d.agent)?.id ?? "";

  const redraft = async (d: Draft) => {
    const agentID = redo[d.id] ?? agentIdOf(d);
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
    if (processing) {
      return (
        <div className="empty">
          <span className="spinner" aria-hidden="true" />
          <strong>正在出稿…</strong>
          <p>模型 / agent 跑完就会出现在这里，不用手动刷新。</p>
        </div>
      );
    }
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

      {repos.length > 1 && (
        <div className="chips">
          <button data-active={repoFilter === ""} onClick={() => setRepoFilter("")}>
            全部仓库<em>{drafts.length}</em>
          </button>
          {repos.map((repo) => (
            <button key={repo} data-active={repoFilter === repo} onClick={() => setRepoFilter(repo)}>
              {repo}
              <em>{drafts.filter((d) => d.repo === repo).length}</em>
            </button>
          ))}
        </div>
      )}

      {pending.length === 0 && (
        <div className="empty">
          <InboxIcon size={34} />
          <strong>{repoFilter ? repoFilter + " 没有待审" : "队列已清空"}</strong>
          <p>{repoFilter ? "切回「全部仓库」看看别的仓库。" : "新命中会实时出现在这里，不用手动刷新。"}</p>
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
                {d.kind && (
                  <span className="tag kind" data-kind={d.kind}>
                    {d.kind === "pr" ? "PR" : "Issue"}
                  </span>
                )}
                {d.type === "bug" && <span className="tag type-bug">Bug</span>}
                {d.type === "feature" && <span className="tag type-feature">Feature</span>}
                {d.merge && <span className="tag merge">可合并 PR</span>}
                {d.mergedTo && <span className="tag merged">已合并到 {d.mergedTo}</span>}
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

            {!isEditing && d.changes && (
              <>
                <Changes changes={d.changes} />
                {d.mergedTo ? (
                  <p className="commit-hint">
                    改动已自动合并到 <code>{d.mergedTo}</code>（分支 <code>{d.branch}</code>），批准只发回复
                  </p>
                ) : d.commit ? (
                  <p className="commit-hint">
                    改动已自动提交到 <code>{d.branch}</code>（{d.commit.slice(0, 7)}），批准只发回复
                  </p>
                ) : (
                  <p className="commit-hint">
                    批准时会把这几处改动提交到分支{" "}
                    <code>gh-agent/issue-{d.number}</code>（只提交到本地，不推送）
                  </p>
                )}
              </>
            )}

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
                    value={redo[d.id] ?? agentIdOf(d)}
                    onChange={(e) => setRedo((prev) => ({ ...prev, [d.id]: e.target.value }))}
                    title="重做用的 agent，默认是产出这条草稿的那个"
                  >
                    {!(redo[d.id] ?? agentIdOf(d)) && <option value="">默认 agent</option>}
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
                    disabled={working}
                    title="用选中的 agent 重新审一遍这条，结果覆盖这条草稿"
                  >
                    <RefreshIcon size={14} />
                    {kind === "redraft" ? "重做中…" : "重做"}
                  </button>
                  <span className="spacer" />
                  <button className="ghost" onClick={() => startIgnore(d)} disabled={working}>
                    <EyeOffIcon size={14} />
                    {kind === "ignore" ? "忽略中…" : "忽略"}
                  </button>
                  <button className="danger" onClick={() => act(d.id, false)} disabled={working}>
                    <XIcon size={14} />
                    {kind === "reject" ? "驳回中…" : "驳回"}
                  </button>
                  <button className="primary" onClick={() => act(d.id, true)} disabled={working}>
                    <CheckIcon size={14} />
                    {kind === "approve"
                      ? "处理中…"
                      : d.merge
                        ? "批准、合并并回复"
                        : "批准并发送"}
                  </button>
                </>
              )}
            </div>

            {ignoring === d.id && (
              <div className="ignore-row">
                <input
                  value={ignoreNote}
                  onChange={(e) => setIgnoreNote(e.target.value)}
                  placeholder="备注（可选）：为什么忽略这条，之后在条目里能看到"
                  spellCheck={false}
                  autoFocus
                />
                <button className="ghost" onClick={() => setIgnoring(null)}>
                  取消
                </button>
                <button className="primary" onClick={() => void doIgnore(d.id)} disabled={working}>
                  确认忽略
                </button>
              </div>
            )}
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
                  {d.note && <em className="hrow-note"> · {d.note}</em>}
                </span>
                {d.branch && <span className="tag branch">{d.branch}</span>}
                {d.status === "ignored" && (
                  <button
                    className="link"
                    onClick={() => void restore(d.id)}
                    disabled={busy === d.id + "|restore"}
                  >
                    恢复
                  </button>
                )}
                <time>{fmtTime(d.createdAt)}</time>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
