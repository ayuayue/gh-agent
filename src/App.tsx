import { useCallback, useEffect, useState } from "react";
import {
  ActivityIcon,
  AlertIcon,
  InboxIcon,
  ListIcon,
  PauseIcon,
  PlayIcon,
  RefreshIcon,
  SlidersIcon,
  type IconProps,
} from "./icons";
import {
  events,
  Service,
  type ConfigDTO,
  type Draft,
  type ItemsSnapshot,
  type LogLine,
  type StatusDTO,
  type TaskDTO,
} from "./mygo";
import Drafts from "./views/Drafts";
import Items from "./views/Items";
import Logs from "./views/Logs";
import Settings from "./views/Settings";

type Tab = "drafts" | "items" | "logs" | "settings";

const TABS: { id: Tab; label: string; icon: (p: IconProps) => React.ReactElement }[] = [
  { id: "drafts", label: "待审", icon: InboxIcon },
  { id: "items", label: "条目", icon: ListIcon },
  { id: "logs", label: "事件流", icon: ActivityIcon },
  { id: "settings", label: "设置", icon: SlidersIcon },
];

const TITLES: Record<Tab, { title: string; hint: string }> = {
  drafts: { title: "待审", hint: "模型出稿后停在这里，你点了批准才会发到 GitHub" },
  items: { title: "条目", hint: "轮询覆盖到的 issue / PR：谁回复过、有没有提交、要不要忽略" },
  logs: { title: "事件流", hint: "轮询、命中、出稿的实时记录" },
  settings: { title: "设置", hint: "改动保存后立即生效，正在跑的话会用新配置重启轮询" },
};

export default function App() {
  const [status, setStatus] = useState<StatusDTO | null>(null);
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [config, setConfig] = useState<ConfigDTO | null>(null);
  const [itemsSnapshot, setItemsSnapshot] = useState<ItemsSnapshot>({ items: [], loading: false });
  const [tab, setTab] = useState<Tab>("drafts");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [itemsAsked, setItemsAsked] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [s, d, l, c, it] = await Promise.all([
        Service.status(),
        Service.drafts(),
        Service.logs(),
        Service.config(),
        Service.items(),
      ]);
      setStatus(s);
      setDrafts(d);
      setLogs(l);
      setConfig(c);
      setItemsSnapshot(it);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  // 条目列表按需拉取：第一次打开这个页签时才去请求 GitHub。
  useEffect(() => {
    if (tab !== "items" || itemsAsked) return;
    setItemsAsked(true);
    Service.refreshItems()
      .then(setItemsSnapshot)
      .catch(() => {});
  }, [tab, itemsAsked]);

  const refreshItems = useCallback(async () => {
    try {
      setItemsSnapshot(await Service.refreshItems());
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Go 侧推过来的增量：状态、新草稿、日志。
  useEffect(() => {
    const offs = [
      events.status.on((s) => setStatus(s)),
      events.draft.on((d) =>
        setDrafts((prev) => {
          const i = prev.findIndex((x) => x.id === d.id);
          if (i === -1) return [d, ...prev];
          const next = prev.slice();
          next[i] = d;
          return next;
        }),
      ),
      events.log.on((l) => setLogs((prev) => [...prev, l].slice(-500))),
      events.items.on((s) => setItemsSnapshot(s)),
    ];
    return () => offs.forEach((off) => off());
  }, []);

  const toggle = async () => {
    if (!status) return;
    setBusy(true);
    setError("");
    try {
      if (status.running) await Service.stop();
      else await Service.start();
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // 处理中的耗时每秒走一格，不用后端反复推状态。
  const tasks = status?.tasks ?? [];
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (tasks.length === 0) return;
    setNow(Date.now());
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [tasks.length]);

  const elapsedOf = (task: TaskDTO) => {
    const started = Date.parse(task.startedAt);
    return Number.isNaN(started) ? 0 : Math.max(0, Math.floor((now - started) / 1000));
  };

  const stopTask = async (task: TaskDTO) => {
    try {
      await Service.stopTask(task.repo, task.number);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const pending = drafts.filter((d) => d.status === "pending").length;
  const meta = TITLES[tab];

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">G</span>
          <span className="brand-name">GH Agent</span>
        </div>

        <nav className="nav">
          {TABS.map(({ id, label, icon: Icon }) => (
            <button key={id} data-active={tab === id} onClick={() => setTab(id)}>
              <Icon size={16} />
              <span>{label}</span>
              {id === "drafts" && pending > 0 && <span className="count">{pending}</span>}
            </button>
          ))}
        </nav>

        <div className="sidebar-foot">
          <div className="status-line">
            <span className="dot" data-on={status?.running || tasks.length > 0 ? "1" : "0"} />
            <span>{tasks.length > 0 ? "处理中" : status?.running ? "轮询中" : "已停止"}</span>
          </div>
          <span className="ellipsis" title={config?.repos.join(", ")}>
            {config?.repos.join(", ") || "未配置仓库"}
          </span>
          <span className="ellipsis">
            {status?.lastPoll ? "上次 " + status.lastPoll : "还没跑过"}
          </span>
        </div>
      </aside>

      <section className="main">
        <header className="topbar">
          <div>
            <h1>{meta.title}</h1>
            <p>{meta.hint}</p>
          </div>
          <div className="actions">
            <button className="ghost icon-only" onClick={() => void refresh()} title="刷新">
              <RefreshIcon size={16} />
            </button>
            <button
              className={status?.running ? "danger" : "primary"}
              onClick={toggle}
              disabled={busy}
            >
              {status?.running ? <PauseIcon size={14} /> : <PlayIcon size={14} />}
              {status?.running ? "停止" : "开始轮询"}
            </button>
          </div>
        </header>

        {(error || status?.lastError) && (
          <div className="banner error">
            <AlertIcon size={15} />
            <span>{error || status?.lastError}</span>
          </div>
        )}

        {tasks.length > 0 && (
          <div className="task-bar">
            {tasks.map((task) => (
              <div className="task-row" key={task.repo + "#" + task.number}>
                <span className="spinner" aria-hidden="true" />
                <span>
                  正在处理 <b>{task.repo}#{task.number}</b>
                  <span className="sep">·</span>
                  {task.agent}
                  <span className="sep">·</span>
                  已 {elapsedOf(task)}s
                </span>
                <button className="link" onClick={() => void stopTask(task)}>
                  停止
                </button>
                {task.commentUrl && (
                  <button className="link" onClick={() => void Service.openExternal(task.commentUrl)}>
                    看原评论
                  </button>
                )}
              </div>
            ))}
          </div>
        )}

        <div className="scroll">
          {tab === "drafts" && (
            <Drafts drafts={drafts} processing={tasks.length > 0} onChanged={refresh} />
          )}
          {tab === "items" && (
            <Items
              snapshot={itemsSnapshot}
              tasks={tasks}
              onRefresh={() => void refreshItems()}
              onSnapshot={setItemsSnapshot}
            />
          )}
          {tab === "logs" && <Logs lines={logs} />}
          {tab === "settings" && config && (
            <Settings
              config={config}
              onSaved={(c) => {
                setConfig(c);
                void refresh();
              }}
            />
          )}
        </div>
      </section>
    </div>
  );
}
