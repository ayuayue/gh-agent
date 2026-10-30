import { useState } from "react";
import { Service, type ConfigDTO } from "../mygo";

export default function Settings({
  config,
  onSaved,
}: {
  config: ConfigDTO;
  onSaved: (c: ConfigDTO) => void;
}) {
  const [form, setForm] = useState(config);
  const [msg, setMsg] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof ConfigDTO>(key: K, value: ConfigDTO[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

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
        <header>仓库与轮询</header>
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
            <span className="hint">多个仓库用逗号分隔</span>
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
              spellCheck={false}
            />
            <span className="hint">评论里出现它就处理</span>
          </label>

          <label className="field">
            <span>每轮最多处理</span>
            <input
              type="number"
              min={1}
              value={form.maxPerPoll}
              onChange={(e) => set("maxPerPoll", Number(e.target.value))}
            />
            <span className="hint">防止一次刷太多</span>
          </label>
        </div>
      </section>

      <section className="field-group">
        <header>模型</header>
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
