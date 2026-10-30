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
        桌面壳只会出稿、不会自己发帖：命中的评论进「待审」，由你点批准。想全自动发帖用 CLI 的
        <code>--live</code>。
      </p>

      <label>
        <span>仓库</span>
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
        />
      </label>

      <div className="grid">
        <label>
          <span>轮询间隔（秒）</span>
          <input
            type="number"
            min={15}
            value={form.intervalSeconds}
            onChange={(e) => set("intervalSeconds", Number(e.target.value))}
          />
        </label>
        <label>
          <span>触发词</span>
          <input value={form.trigger} onChange={(e) => set("trigger", e.target.value)} />
        </label>
        <label>
          <span>每轮最多处理</span>
          <input
            type="number"
            min={1}
            value={form.maxPerPoll}
            onChange={(e) => set("maxPerPoll", Number(e.target.value))}
          />
        </label>
      </div>

      <h3>模型</h3>
      <div className="grid">
        <label>
          <span>端点</span>
          <input value={form.baseUrl} onChange={(e) => set("baseUrl", e.target.value)} />
        </label>
        <label>
          <span>模型</span>
          <input value={form.model} onChange={(e) => set("model", e.target.value)} />
        </label>
        <label>
          <span>API key 环境变量</span>
          <input value={form.apiKeyEnv} onChange={(e) => set("apiKeyEnv", e.target.value)} />
        </label>
        <label>
          <span>超时（秒）</span>
          <input
            type="number"
            min={10}
            value={form.timeoutSeconds}
            onChange={(e) => set("timeoutSeconds", Number(e.target.value))}
          />
        </label>
        <label>
          <span>最大输出 token</span>
          <input
            type="number"
            min={128}
            value={form.maxOutputTokens}
            onChange={(e) => set("maxOutputTokens", Number(e.target.value))}
          />
        </label>
        <label>
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

      <label>
        <span>署名（追加在每条回复末尾，可留空）</span>
        <input value={form.signature} onChange={(e) => set("signature", e.target.value)} />
      </label>

      <footer>
        <span className="muted grow">{form === config ? "" : "有未保存的改动"}</span>
        {error && <span className="err">{error}</span>}
        {msg && <span className="ok">{msg}</span>}
        <button className="primary" type="submit" disabled={busy}>
          {busy ? "保存中…" : "保存"}
        </button>
      </footer>
    </form>
  );
}
