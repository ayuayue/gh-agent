import { useCallback, useEffect, useState } from "react";
import { events, Service, type ConfigDTO, type Draft, type LogLine, type StatusDTO } from "./mygo";
import Drafts from "./views/Drafts";
import Logs from "./views/Logs";
import Settings from "./views/Settings";

type Tab = "drafts" | "logs" | "settings";

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

  return (
    <div className="app">
      <header className="bar">
        <div className="brand">
          <span className="dot" data-on={status?.running ? "1" : "0"} />
          <strong>GH Agent</strong>
          <span className="muted">{status?.running ? "轮询中" : "已停止"}</span>
        </div>
        <div className="meta">
          <span className="muted">{config?.repos.join(", ") || "未配置仓库"}</span>
          {status?.me && <span className="muted">· {status.me}</span>}
          {status?.lastPoll && <span className="muted">· 上次 {status.lastPoll}</span>}
          {status?.handled ? <span className="muted">· 累计 {status.handled}</span> : null}
        </div>
        <button className="primary" onClick={toggle} disabled={busy}>
          {status?.running ? "停止" : "开始"}
        </button>
      </header>

      {(error || status?.lastError) && (
        <div className="alert">{error || status?.lastError}</div>
      )}

      <nav className="tabs">
        <button data-active={tab === "drafts"} onClick={() => setTab("drafts")}>
          待审 {pending > 0 && <em>{pending}</em>}
        </button>
        <button data-active={tab === "logs"} onClick={() => setTab("logs")}>
          事件流
        </button>
        <button data-active={tab === "settings"} onClick={() => setTab("settings")}>
          设置
        </button>
      </nav>

      <main className="content">
        {tab === "drafts" && <Drafts drafts={drafts} onChanged={refresh} />}
        {tab === "logs" && <Logs lines={logs} />}
        {tab === "settings" && config && (
          <Settings config={config} onSaved={(c) => { setConfig(c); void refresh(); }} />
        )}
      </main>
    </div>
  );
}
