import DOMPurify from "dompurify";
import { marked } from "marked";
import { useMemo } from "react";

/** 把模型出的 markdown 渲染成安全的 HTML。 */
export default function Markdown({ text }: { text: string }) {
  const html = useMemo(() => {
    const raw = marked.parse(text, { async: false }) as string;
    return DOMPurify.sanitize(raw);
  }, [text]);
  return <div className="md" dangerouslySetInnerHTML={{ __html: html }} />;
}
