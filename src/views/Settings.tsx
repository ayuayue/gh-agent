import { useEffect, useState } from "react";
import { CheckIcon, XIcon } from "../icons";
import {
  Service,
  type AgentInfo,
  type AgentProbe,
  type ConfigDTO,
  type PathCheck,
} from "../mygo";

const PERMISSIONS = [
  { id: "bypass", name: "全部放行", desc: "读文件、改代码、跑命令、联网都直接允许" },
  { id: "write", name: "读写文件", desc: "允许读和改文件，禁止执行命令与联网" },
  { id: "read", name: "只读", desc: "只允许读，禁止改文件和执行命令" },
  { id: "deny", name: "全部拒绝", desc: "工具全禁，agent 只能靠 prompt 里的上下文回答" },
];

export default function Settings({
  config,
  onSaved,
}: {
  config: ConfigDTO;
  onSaved: (c: ConfigDTO) => void;
}) {
  const [form, setForm] = useState(config);
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [checks, setChecks] = useState<Record<string, PathCheck>>({});
  const [newRepo, setNewRepo] = useState("");
  const [newPath, setNewPath] = useState("");
  const [msg, setMsg] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [probes, setProbes] = useState<Record<string, AgentProbe>>({});
  const [probing, setProbing] = useState("");

  useEffect(() => {
    Service.agents()
      .then(setAgents)
      .catch(() => setAgents([]));
  }, []);

  const probeAgent = async (id: string, refresh = false) => {
    if (!id || probing) return;
    setProbing(id);
    try {
      const result = await Service.probeAgent(id, refresh);
      setProbes((prev) => ({ ...prev, [id]: result }));
    } catch (err) {
      setProbes((prev) => ({
        ...prev,
        [id]: { id, ok: false, info: {}, detail: err instanceof Error ? err.message : String(err) },
      }));
    } finally {
      setProbing("");
    }
  };

  // 选中的 agent 自动读一次参数，省得每次手动点；服务端有缓存。
  useEffect(() => {
    if (form.agent && !probes[form.agent]) void probeAgent(form.agent);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form.agent]);

  const set = <K extends keyof ConfigDTO>(key: K, value: ConfigDTO[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  // 目录一变就重新检查：是不是存在、是不是 git 仓库。
  const pathKey = Object.values(form.repoPaths).join("|");
  useEffect(() => {
    let alive = true;
    const paths = [...new Set(Object.values(form.repoPaths).filter(Boolean))];
    Promise.all(paths.map((p) => Service.checkPath(p).then((r) => [p, r] as const)))
      .then((pairs) => {
        if (!alive) return;
        setChecks(Object.fromEntries(pairs));
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pathKey]);

  // 改名时把本地目录映射一起挪过去，不然路径会留在旧名字下面。
  const renameRepo = (index: number, next: string) =>
    setForm((f) => {
      const prev = f.repos[index];
      if (prev === undefined) return f;
      const repos = f.repos.map((r, i) => (i === index ? next : r));
      const paths = { ...f.repoPaths };
      if (prev !== next && paths[prev] !== undefined) {
        paths[next] = paths[prev];
        delete paths[prev];
      }
      return { ...f, repos, repoPaths: paths };
    });

  const setRepoPath = (repo: string, path: string) =>
    setForm((f) => {
      const paths = { ...f.repoPaths };
      if (path.trim()) paths[repo] = path;
      else delete paths[repo];
      return { ...f, repoPaths: paths };
    });

  const removeRepo = (index: number) =>
    setForm((f) => {
      const repo = f.repos[index];
      if (repo === undefined) return f;
      const paths = { ...f.repoPaths };
      delete paths[repo];
      return { ...f, repos: f.repos.filter((_, i) => i !== index), repoPaths: paths };
    });

  const addRepo = () => {
    const repo = newRepo.trim();
    if (!repo) return;
    if (form.repos.some((r) => r.toLowerCase() === repo.toLowerCase())) {
      setNewRepo("");
      setNewPath("");
      return;
    }
    const path = newPath.trim();
    setForm((f) => {
      const paths = { ...f.repoPaths };
      if (path) paths[repo] = path;
      return { ...f, repos: [...f.repos, repo], repoPaths: paths };
    });
    setNewRepo("");
    setNewPath("");
  };

  const dirty = JSON.stringify(form) !== JSON.stringify(config);

  const save = async () => {
    setBusy(true);
    setError("");
    setMsg("");
    try {
      // 列表里可能留着空行、重复项：保存前收一遍，别让它们把校验顶回来。
      const seen = new Set<string>();
      const repos = form.repos.map((r) => r.trim()).filter((r) => {
        if (!r) return false;
        const key = r.toLowerCase();
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
      // 只保留还在列表里的仓库的目录，空路径不落盘。
      const repoPaths: Record<string, string> = {};
      for (const repo of repos) {
        const path = (form.repoPaths[repo] ?? "").trim();
        if (path) repoPaths[repo] = path;
      }
      const saved = await Service.saveConfig({ ...form, repos, repoPaths });
      setForm(saved);
      onSaved(saved);
      setMsg("已保存");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      className="settings"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <p className="note">
        桌面壳只出稿、不自己发帖：命中的评论进「待审」，由你点批准。要全自动发帖，用 CLI 的
        <code>--live</code>。
      </p>

      <section className="field-group">
        <header>出稿方</header>
        <div className="body">
          <div className="agent-list">
            {agents.map((a) => (
              <label
                key={a.id}
                className="agent-option"
                data-selected={form.agent === a.id}
                data-disabled={!a.available}
              >
                <input
                  type="radio"
                  name="agent"
                  value={a.id}
                  checked={form.agent === a.id}
                  disabled={!a.available}
                  onChange={() => set("agent", a.id)}
                />
                <span className="agent-body">
                  <span className="agent-name">
                    {a.name}
                    {a.available && (
                      <button
                        type="button"
                        className="link agent-probe"
                        onClick={(e) => {
                          e.preventDefault();
                          e.stopPropagation();
                          void probeAgent(a.id, true);
                        }}
                        disabled={probing === a.id}
                      >
                        {probing === a.id ? "读取中…" : "读取参数"}
                      </button>
                    )}
                    {form.agent === a.id && <CheckIcon size={13} />}
                  </span>
                  <span className="agent-desc">{a.desc}</span>
                  {a.detail && <span className="agent-detail">{a.detail}</span>}
                  {(() => {
                    const probe = probes[a.id];
                    if (!probe) return null;
                    if (!probe.ok) {
                      return <span className="agent-detail">读取失败：{probe.detail}</span>;
                    }
                    const models = probe.info.models ?? [];
                    const caps = probe.info.capabilities ?? [];
                    const modes = probe.info.modes ?? [];
                    return (
                      <>
                        {probe.info.currentModel && (
                          <span className="agent-params">
                            默认模型 <code>{probe.info.currentModel}</code>
                          </span>
                        )}
                        {models.length > 0 && (
                          <span className="agent-params" title={models.join("\n")}>
                            可用模型 {models.length} 个：{models.slice(0, 5).join(" / ")}
                            {models.length > 5 ? " …" : ""}
                          </span>
                        )}
                        {modes.length > 0 && (
                          <span className="agent-params">
                            模式 {modes.slice(0, 5).join(" / ")}
                            {modes.length > 5 ? " …" : ""}
                          </span>
                        )}
                        {caps.length > 0 && (
                          <span className="agent-params">能力 {caps.join("、")}</span>
                        )}
                        {!probe.info.currentModel && models.length === 0 && caps.length === 0 && (
                          <span className="agent-params">没报模型和能力</span>
                        )}
                      </>
                    );
                  })()}
                </span>
              </label>
            ))}
          </div>
          <span className="hint" style={{ gridColumn: "1 / -1" }}>
            {form.agent === ""
              ? "还没选出稿方：选一个才能开始轮询和审查。"
              : "四个 agent 都跑在本机进程里，能读代码、跑命令；「读取参数」会真起一次进程，读出默认模型和能力。"}
          </span>
        </div>
      </section>

      <section className="field-group">
        <header>工具权限（只对本地 agent 生效）</header>
        <div className="body">
          <div className="agent-list">
            {PERMISSIONS.map((p) => (
              <label
                key={p.id}
                className="agent-option"
                data-selected={form.agentPermission === p.id}
              >
                <input
                  type="radio"
                  name="permission"
                  value={p.id}
                  checked={form.agentPermission === p.id}
                  onChange={() => set("agentPermission", p.id)}
                />
                <span className="agent-body">
                  <span className="agent-name">
                    {p.name}
                    <code className="perm-id">{p.id}</code>
                    {form.agentPermission === p.id && <CheckIcon size={13} />}
                  </span>
                  <span className="agent-desc">{p.desc}</span>
                </span>
              </label>
            ))}
          </div>
          <span className="hint" style={{ gridColumn: "1 / -1" }}>
            只能约束「会主动询问」的 agent：CodeBuddy 每次都问，pi-acp 内部直接放行，
            设成只读也拦不住它改文件——那种情况只能靠出稿后的 diff 看它做了什么。
          </span>
        </div>
      </section>

      <section className="field-group">
        <header>仓库</header>
        <div className="body">
          <div className="map-list">
            {form.repos.length === 0 && (
              <p className="map-empty">还没有仓库。加一个 owner/name 就会开始监控。</p>
            )}
            {form.repos.map((repo, i) => {
              const path = form.repoPaths[repo] ?? "";
              const check = path ? checks[path] : undefined;
              return (
                <div className="map-row" key={i}>
                  <input
                    className="repo-name"
                    value={repo}
                    onChange={(e) => renameRepo(i, e.target.value)}
                    placeholder="owner/name"
                    spellCheck={false}
                  />
                  <input
                    value={path}
                    onChange={(e) => setRepoPath(repo, e.target.value)}
                    placeholder="本地目录（可选，agent 读代码用）"
                    spellCheck={false}
                  />
                  <span
                    className={
                      check ? (check.isGit ? "map-state ok" : "map-state warn") : "map-state"
                    }
                    title={check?.detail ?? ""}
                  >
                    {check ? (check.isGit ? "git 仓库" : check.detail) : ""}
                  </span>
                  <button
                    type="button"
                    className="ghost icon-only"
                    onClick={() => removeRepo(i)}
                    title="移除仓库"
                  >
                    <XIcon size={14} />
                  </button>
                </div>
              );
            })}
          </div>

          <div className="map-row map-add">
            <input
              value={newRepo}
              onChange={(e) => setNewRepo(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addRepo();
                }
              }}
              placeholder="owner/name"
              spellCheck={false}
            />
            <input
              value={newPath}
              onChange={(e) => setNewPath(e.target.value)}
              placeholder="本地目录（可选）"
              spellCheck={false}
            />
            <button type="button" onClick={addRepo} disabled={!newRepo.trim()}>
              添加
            </button>
          </div>

          <span className="hint" style={{ gridColumn: "1 / -1" }}>
            列表里的仓库都会被监控；填了本地目录的，本地 agent 才能读代码、改代码。目录不存在或不是 git
            仓库会在右边标出来。
          </span>
        </div>
      </section>

      <section className="field-group">
        <header>轮询</header>
        <div className="body">
          <label className="field">
            <span>轮询间隔</span>
            <input
              type="number"
              min={15}
              value={form.intervalSeconds}
              onChange={(e) => set("intervalSeconds", Number(e.target.value))}
            />
            <span className="hint">秒，最小 15</span>
          </label>

          <label className="field">
            <span>触发词</span>
            <input
              value={form.trigger}
              onChange={(e) => set("trigger", e.target.value)}
              placeholder="留空 = 全部处理"
              spellCheck={false}
            />
            <span className={form.trigger.trim() === "" ? "hint warn" : "hint"}>
              {form.trigger.trim() === ""
                ? "留空：任何 issue / PR 评论都会处理"
                : "评论里出现这个词才处理"}
            </span>
          </label>

          <label className="field">
            <span>每轮最多处理</span>
            <input
              type="number"
              min={1}
              value={form.maxPerPoll}
              onChange={(e) => set("maxPerPoll", Number(e.target.value))}
            />
          </label>
        </div>
      </section>

      <section className="field-group">
        <header>输出</header>
        <div className="body">
          <label className="field" style={{ gridColumn: "1 / -1" }}>
            <span>署名</span>
            <input
              value={form.signature}
              onChange={(e) => set("signature", e.target.value)}
              placeholder="留空则不加"
            />
            <span className="hint">追加在每条回复末尾</span>
          </label>
        </div>
      </section>

      <div className="settings-bar">
        <span className="grow">{dirty ? "有未保存的改动" : "配置已是最新"}</span>
        {error && <span className="err">{error}</span>}
        {msg && !dirty && <span className="ok">{msg}</span>}
        <button className="primary" type="submit" disabled={busy || !dirty}>
          {busy ? "保存中…" : "保存"}
        </button>
      </div>
    </form>
  );
}
