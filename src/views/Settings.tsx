import { useEffect, useMemo, useState } from "react";
import { CheckIcon, XIcon } from "../icons";
import { Service, type AgentInfo, type ConfigDTO, type PathCheck } from "../mygo";

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

  useEffect(() => {
    Service.agents()
      .then(setAgents)
      .catch(() => setAgents([]));
  }, []);

  const set = <K extends keyof ConfigDTO>(key: K, value: ConfigDTO[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  const mappings = useMemo(
    () => Object.entries(form.repoPaths).sort(([a], [b]) => a.localeCompare(b)),
    [form.repoPaths],
  );

  // 目录一变就重新检查：是不是存在、是不是 git 仓库。
  const pathKey = mappings.map(([, p]) => p).join("|");
  useEffect(() => {
    let alive = true;
    const paths = [...new Set(mappings.map(([, p]) => p).filter(Boolean))];
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

  const setPath = (repo: string, path: string) =>
    setForm((f) => ({ ...f, repoPaths: { ...f.repoPaths, [repo]: path } }));

  const removePath = (repo: string) =>
    setForm((f) => {
      const next = { ...f.repoPaths };
      delete next[repo];
      return { ...f, repoPaths: next };
    });

  const addMapping = () => {
    const repo = newRepo.trim();
    const path = newPath.trim();
    if (!repo || !path) return;
    setPath(repo, path);
    setNewRepo("");
    setNewPath("");
  };

  const dirty = JSON.stringify(form) !== JSON.stringify(config);

  const save = async () => {
    setBusy(true);
    setError("");
    setMsg("");
    try {
      const saved = await Service.saveConfig(form);
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
                    {form.agent === a.id && <CheckIcon size={13} />}
                  </span>
                  <span className="agent-desc">{a.desc}</span>
                  {a.detail && <span className="agent-detail">{a.detail}</span>}
                </span>
              </label>
            ))}
          </div>
          <span className="hint" style={{ gridColumn: "1 / -1" }}>
            本地 agent 能读仓库、跑命令，出稿更准但慢得多；模型端点只看到 prompt 里的上下文。
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
        <header>仓库 → 本地目录</header>
        <div className="body">
          <div className="map-list">
            {mappings.length === 0 && (
              <p className="map-empty">还没有映射。本地 agent 要有本地克隆才能读代码。</p>
            )}
            {mappings.map(([repo, path]) => {
              const check = checks[path];
              return (
                <div className="map-row" key={repo}>
                  <code className="map-repo">{repo}</code>
                  <input
                    value={path}
                    onChange={(e) => setPath(repo, e.target.value)}
                    placeholder="D:/project/xxx"
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
                    onClick={() => removePath(repo)}
                    title="移除映射"
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
              placeholder="owner/name"
              spellCheck={false}
            />
            <input
              value={newPath}
              onChange={(e) => setNewPath(e.target.value)}
              placeholder="D:/project/xxx"
              spellCheck={false}
            />
            <button type="button" onClick={addMapping} disabled={!newRepo.trim() || !newPath.trim()}>
              添加
            </button>
          </div>

          <span className="hint" style={{ gridColumn: "1 / -1" }}>
            可以映射任意多个仓库。没映射的仓库会用配置目录下的空目录，agent 只能靠上下文作答。
          </span>
        </div>
      </section>

      <section className="field-group">
        <header>轮询</header>
        <div className="body">
          <label className="field" style={{ gridColumn: "1 / -1" }}>
            <span>监控的仓库</span>
            <input
              value={form.repos.join(", ")}
              onChange={(e) =>
                set(
                  "repos",
                  e.target.value
                    .split(",")
                    .map((s) => s.trim())
                    .filter(Boolean),
                )
              }
              placeholder="owner/name, owner/name"
              spellCheck={false}
            />
            <span className="hint">多个仓库用逗号分隔，跟上面的映射独立</span>
          </label>

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
        <header>模型端点（agent 选 llm 时才用）</header>
        <div className="body">
          <label className="field">
            <span>端点</span>
            <input
              value={form.baseUrl}
              onChange={(e) => set("baseUrl", e.target.value)}
              spellCheck={false}
            />
            <span className="hint">OpenAI 兼容，带 /v1</span>
          </label>

          <label className="field">
            <span>模型</span>
            <input
              value={form.model}
              onChange={(e) => set("model", e.target.value)}
              spellCheck={false}
            />
          </label>

          <label className="field">
            <span>API key 环境变量</span>
            <input
              value={form.apiKeyEnv}
              onChange={(e) => set("apiKeyEnv", e.target.value)}
              spellCheck={false}
            />
            <span className="hint">key 本身不落盘</span>
          </label>

          <label className="field">
            <span>超时</span>
            <input
              type="number"
              min={10}
              value={form.timeoutSeconds}
              onChange={(e) => set("timeoutSeconds", Number(e.target.value))}
            />
            <span className="hint">秒</span>
          </label>

          <label className="field">
            <span>最大输出 token</span>
            <input
              type="number"
              min={128}
              value={form.maxOutputTokens}
              onChange={(e) => set("maxOutputTokens", Number(e.target.value))}
            />
            <span className="hint">太小会把正文截断</span>
          </label>

          <label className="field">
            <span>temperature</span>
            <input
              type="number"
              step="0.1"
              min={0}
              max={2}
              value={form.temperature}
              onChange={(e) => set("temperature", Number(e.target.value))}
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
