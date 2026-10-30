import { useCallback, useEffect, useState } from "react";
import {
  ActivityIcon,
  AlertIcon,
  InboxIcon,
  PauseIcon,
  PlayIcon,
  RefreshIcon,
  SlidersIcon,
  type IconProps,
} from "./icons";
import { events, Service, type ConfigDTO, type Draft, type LogLine, type StatusDTO } from "./mygo";
import Drafts from "./views/Drafts";
import Logs from "./views/Logs";
import Settings from "./views/Settings";

type Tab = "drafts" | "logs" | "settings";

const TABS: { id: Tab; label: string; icon: (p: IconProps) => React.ReactElement }[] = [
  { id: "drafts", label: "待审", icon: InboxIcon },
  { id: "logs", label: "事件流", icon: ActivityIcon },
  { id: "settings", label: "设置", icon: SlidersIcon },
];

const TITLES: Record<Tab, { title: string; hint: string }> = {
  drafts: { title: "待审", hint: "模型出稿后停在这里，你点了批准才会发到 GitHub" },
  logs: { title: "事件流", hint: "轮询、命中、出稿的实时记录" },
  settings: { title: "设置", hint: "改动保存后立即生效，正在跑的话会用新配置重启轮询" },
};

export default function App() {
  const [status, setStatus] = useState<StatusDTO | null>(null);
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [config, setConfig] = useState<ConfigDTO | null>(null);
  const [tab, setTab] = useState<Tab>("drafts");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [s, d, l, c] = await Promise.all([
        Service.status(),
        Service.drafts(),
        Service.logs(),
        Service.config(),
      ]);
      setStatus(s);
      setDrafts(d);
      setLogs(l);
      setConfig(c);
      setError("");
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
            <span className="dot" data-on={status?.running ? "1" : "0"} />
            <span>{status?.running ? "轮询中" : "已停止"}</span>
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

        <div className="scroll">
          {tab === "drafts" && <Drafts drafts={drafts} onChanged={refresh} />}
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
