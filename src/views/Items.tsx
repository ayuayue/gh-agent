import { useMemo, useState } from "react";
import {
  ChevronIcon,
  ExternalIcon,
  EyeOffIcon,
  GitCommitIcon,
  ListIcon,
  MessageIcon,
  RefreshIcon,
  SparklesIcon,
} from "../icons";
import Markdown from "../Markdown";
import { Service, type Item, type ItemsSnapshot, type TaskDTO } from "../mygo";

type KindFilter = "all" | "issue" | "pr";
type StatusFilter = "all" | "unreplied" | "replied" | "committed" | "ignored";

const STATUS_FILTERS: { id: StatusFilter; label: string }[] = [
  { id: "all", label: "全部" },
  { id: "unreplied", label: "未回复" },
  { id: "replied", label: "已回复" },
  { id: "committed", label: "已提交" },
  { id: "ignored", label: "已忽略" },
];

function fmtTime(iso?: string) {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, "0");
  return p(d.getMonth() + 1) + "-" + p(d.getDate()) + " " + p(d.getHours()) + ":" + p(d.getMinutes());
}

function matchStatus(item: Item, status: StatusFilter) {
  switch (status) {
    case "unreplied":
      return !item.replied && !item.ignored;
    case "replied":
      return item.replied;
    case "committed":
      return item.committed;
    case "ignored":
      return item.ignored;
    default:
      return true;
  }
}

export default function Items({
  snapshot,
  tasks,
  onRefresh,
  onSnapshot,
}: {
  snapshot: ItemsSnapshot;
  tasks: TaskDTO[];
  onRefresh: () => void;
  onSnapshot: (s: ItemsSnapshot) => void;
}) {
  const [kind, setKind] = useState<KindFilter>("all");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [repoFilter, setRepoFilter] = useState("");
  const [expanded, setExpanded] = useState<string[]>([]);
  const [ignoring, setIgnoring] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  const items = snapshot.items ?? [];
  const repos = useMemo(() => [...new Set(items.map((i) => i.repo))].sort(), [items]);
  // 仓库筛选是外层：下面的计数、分组都只在选中的仓库里算。
  const scoped = repoFilter ? items.filter((i) => i.repo === repoFilter) : items;

  const counts = useMemo(
    () => ({
      all: scoped.length,
      issue: scoped.filter((i) => i.kind === "issue").length,
      pr: scoped.filter((i) => i.kind === "pr").length,
      unreplied: scoped.filter((i) => matchStatus(i, "unreplied")).length,
      replied: scoped.filter((i) => i.replied).length,
      committed: scoped.filter((i) => i.committed).length,
      ignored: scoped.filter((i) => i.ignored).length,
    }),
    [scoped],
  );

  const visible = scoped.filter(
    (item) => (kind === "all" || item.kind === kind) && matchStatus(item, status),
  );

  const groups =
    kind === "all"
      ? [
          { id: "issue", label: "Issue", rows: visible.filter((i) => i.kind === "issue") },
          { id: "pr", label: "PR", rows: visible.filter((i) => i.kind === "pr") },
        ]
      : [{ id: kind, label: kind === "pr" ? "PR" : "Issue", rows: visible }];

  const toggle = (id: string) =>
    setExpanded((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));

  const startIgnore = (item: Item) => {
    setIgnoring(item.id);
    setNote(item.ignoreNote ?? "");
    setError("");
  };

  const confirmIgnore = async (item: Item) => {
    setBusy(item.id);
    setError("");
    try {
      onSnapshot(await Service.ignoreItem(item.repo, item.number, note));
      setIgnoring(null);
      setNote("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  // 手动审查：不用等轮询，直接让 agent 分析这条并出稿，结果照样进待审。
  const review = async (item: Item) => {
    setBusy(item.id);
    setError("");
    try {
      await Service.reviewItem(item.repo, item.number);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  const undoIgnore = async (item: Item) => {
    setBusy(item.id);
    setError("");
    try {
      onSnapshot(await Service.unignoreItem(item.repo, item.number));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="items">
      <div className="items-bar">
        <div className="segmented">
          {(["all", "issue", "pr"] as KindFilter[]).map((id) => (
            <button key={id} data-active={kind === id} onClick={() => setKind(id)}>
              {id === "all" ? "全部" : id === "pr" ? "PR" : "Issue"}
              <em>{counts[id]}</em>
            </button>
          ))}
        </div>

        {repos.length > 1 && (
          <select
            className="agent-select"
            value={repoFilter}
            onChange={(e) => setRepoFilter(e.target.value)}
            title="按仓库筛选"
          >
            <option value="">全部仓库</option>
            {repos.map((repo) => (
              <option key={repo} value={repo}>
                {repo}
              </option>
            ))}
          </select>
        )}

        <div className="chips">
          {STATUS_FILTERS.map((f) => (
            <button key={f.id} data-active={status === f.id} onClick={() => setStatus(f.id)}>
              {f.label}
              {f.id !== "all" && <em>{counts[f.id]}</em>}
            </button>
          ))}
        </div>

        <span className="spacer" />
        <span className="items-meta">
          {snapshot.loading ? "刷新中…" : snapshot.fetchedAt ? "更新于 " + snapshot.fetchedAt : "还没拉过"}
        </span>
        <button className="ghost" onClick={onRefresh} disabled={snapshot.loading}>
          <RefreshIcon size={14} />
          刷新
        </button>
      </div>

      {(error || snapshot.error) && <div className="banner error">{error || snapshot.error}</div>}

      {items.length === 0 && !snapshot.loading && (
        <div className="empty">
          <ListIcon size={34} />
          <strong>还没有条目</strong>
          <p>
            点「刷新」从配置的仓库里拉一次 open 的 issue / PR。条目列表是手动刷新的，不跟着每轮轮询跑。
          </p>
        </div>
      )}

      {items.length > 0 && visible.length === 0 && (
        <div className="empty">
          <ListIcon size={34} />
          <strong>这个筛选下没有条目</strong>
          <p>换一个状态或类型看看。</p>
        </div>
      )}

      {groups.map(
        (group) =>
          group.rows.length > 0 && (
            <section className="item-group" key={group.id}>
              <div className="section-label">
                {group.label} · {group.rows.length}
              </div>
              <div className="item-list">
                {group.rows.map((item) => {
                  const open = expanded.includes(item.id);
                  const working = busy === item.id;
                  const active = tasks.some((t) => t.repo === item.repo && t.number === item.number);
                  return (
                    <article className="item" key={item.id} data-ignored={item.ignored}>
                      <div className="item-main">
                        <span className="item-kind" data-kind={item.kind}>
                          {item.kind === "pr" ? "PR" : "Issue"}
                        </span>
                        <button
                          className="link item-ref"
                          onClick={() => void Service.openExternal(item.url)}
                          title={item.repo + " · 在浏览器打开"}
                        >
                          {item.repo}#{item.number}
                        </button>
                        <span className="item-title" title={item.title}>
                          {item.title}
                        </span>
                        <span className="item-chips">
                          {item.type === "bug" && <span className="tag type-bug">Bug</span>}
                          {item.type === "feature" && <span className="tag type-feature">Feature</span>}
                          {item.replied ? (
                            <span className="tag replied">已回复</span>
                          ) : (
                            <span className="tag unreplied">未回复</span>
                          )}
                          {item.committed && (
                            <span
                              className="tag committed"
                              title={item.branch ? "分支 " + item.branch : undefined}
                            >
                              <GitCommitIcon size={11} />
                              已提交 {item.commit ? item.commit.slice(0, 7) : ""}
                            </span>
                          )}
                          {active && <span className="tag processing">处理中</span>}
                          {item.draftStatus === "pending" && <span className="tag pending">待审</span>}
                          {item.ignored && <span className="tag ignored">已忽略</span>}
                        </span>
                      </div>

                      <div className="item-sub">
                        <span>{item.author || "未知作者"}</span>
                        <span>·</span>
                        <span>{item.comments} 条评论</span>
                        <span>·</span>
                        <span title={"更新 " + fmtTime(item.updatedAt)}>创建 {fmtTime(item.createdAt)}</span>
                        {item.ignoreNote && <span className="item-note">备注：{item.ignoreNote}</span>}
                        <span className="spacer" />
                        <button className="ghost" onClick={() => toggle(item.id)}>
                          <MessageIcon size={13} />
                          {open ? "收起回复" : "看回复"}
                          <ChevronIcon size={12} />
                        </button>
                        <button
                          className="ghost"
                          onClick={() => void review(item)}
                          disabled={working || active}
                          title="立刻让 agent 分析这个条目并出稿，不用等轮询"
                        >
                          <SparklesIcon size={13} />
                          {working ? "审查中…" : "审查"}
                        </button>
                        <button className="ghost" onClick={() => void Service.openExternal(item.url)}>
                          <ExternalIcon size={13} />
                          打开
                        </button>
                        {item.ignored ? (
                          <button className="ghost" onClick={() => void undoIgnore(item)} disabled={working}>
                            {working ? "处理中…" : "取消忽略"}
                          </button>
                        ) : (
                          <button className="ghost" onClick={() => startIgnore(item)} disabled={working}>
                            <EyeOffIcon size={13} />
                            忽略
                          </button>
                        )}
                      </div>

                      {ignoring === item.id && (
                        <div className="item-ignore">
                          <input
                            value={note}
                            onChange={(e) => setNote(e.target.value)}
                            placeholder="备注（可选）：为什么忽略这个条目"
                            spellCheck={false}
                            autoFocus
                          />
                          <button className="ghost" onClick={() => setIgnoring(null)}>
                            取消
                          </button>
                          <button className="primary" onClick={() => void confirmIgnore(item)} disabled={working}>
                            {working ? "保存中…" : "确认忽略"}
                          </button>
                        </div>
                      )}

                      {open && (
                        <div className="item-replies">
                          {item.replies.length === 0 ? (
                            <p className="item-empty-replies">还没有回复。</p>
                          ) : (
                            item.replies.map((reply, i) => (
                              <div className="reply" key={item.id + "-" + i}>
                                <div className="reply-head">
                                  <span className="author">{reply.author || "未知"}</span>
                                  {reply.mine && <span className="tag mine">我</span>}
                                  <time>{fmtTime(reply.createdAt)}</time>
                                  {reply.commit && <span className="tag committed">commit</span>}
                                  {reply.url && (
                                    <button
                                      className="link reply-link"
                                      onClick={() => void Service.openExternal(reply.url)}
                                    >
                                      查看
                                    </button>
                                  )}
                                </div>
                                <div className="reply-body">
                                  <Markdown text={reply.body} />
                                </div>
                              </div>
                            ))
                          )}
                        </div>
                      )}
                    </article>
                  );
                })}
              </div>
            </section>
          ),
      )}

      {items.length > 0 && (
        <p className="items-foot">
          已回复 / 已提交来自 GitHub 上已有的评论和本地草稿记录；条目级忽略只影响之后的轮询，不改动 GitHub。
        </p>
      )}
    </div>
  );
}
